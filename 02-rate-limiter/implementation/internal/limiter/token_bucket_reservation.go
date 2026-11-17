package limiter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	reservationTokenScale          int64 = 1_000_000
	maxLuaExactInteger             int64 = 1<<53 - 1
	reservationTTL                       = 30 * time.Second
	maxReservationCleanupAttempts        = 10
	reservationTerminalRetentionMS int64 = 60 * 60 * 1000
	reservationCleanupBatchSize          = 100
)

var (
	ErrReservationNotFound = errors.New("token bucket reservation not found")
	ErrReservationClosed   = errors.New("token bucket reservation is closed")
)

type TokenBucketReservation struct {
	client               redis.Scripter
	key                  string
	reservationDataKey   string
	indexKey             string
	terminalIndexKey     string
	reservationID        string
	closed               atomic.Bool
	capacityScaled       int64
	refillPerMillisecond float64
}

type TokenBucketReservationLimiter struct {
	client redis.Scripter
}

func NewTokenBucketReservationLimiter(
	client redis.Scripter,
) *TokenBucketReservationLimiter {
	return &TokenBucketReservationLimiter{client: client}
}

// reserveTokenBucketScript reserves quota and recovers expired reservations.
// All keys are explicitly supplied and share one Redis Cluster hash slot.
var reserveTokenBucketScript = redis.NewScript(`
local bucket_key = KEYS[1]
local index_key = KEYS[2]
local data_key = KEYS[3]
local terminal_key = KEYS[4]

local capacity = tonumber(ARGV[1])
local refill_per_ms = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local reservation_ttl_ms = tonumber(ARGV[4])
local reservation_id = ARGV[5]

local cleanup_batch_size = 100
local retention_ms = 3600000

local now = redis.call("TIME")
local now_ms = tonumber(now[1]) * 1000
    + math.floor(tonumber(now[2]) / 1000)

-- Remove terminal metadata whose retention period has elapsed.
local terminal_expired = redis.call(
    "ZRANGEBYSCORE",
    terminal_key,
    "-inf",
    now_ms,
    "LIMIT",
    0,
    cleanup_batch_size
)

for _, id in ipairs(terminal_expired) do
    redis.call(
        "HDEL",
        data_key,
        id .. ":cost",
        id .. ":status",
        id .. ":created_at_ms",
        id .. ":expires_at_ms"
    )
    redis.call("ZREM", terminal_key, id)
end

-- Load and refill the bucket.
local values = redis.call(
    "HMGET",
    bucket_key,
    "tokens",
    "last_refill_ms",
    "reserved"
)

local tokens = tonumber(values[1])
local last_refill_ms = tonumber(values[2])
local reserved = tonumber(values[3]) or 0

if tokens == nil then
    tokens = capacity
    last_refill_ms = now_ms
end

if now_ms > last_refill_ms then
    local elapsed_ms = now_ms - last_refill_ms
    local available_capacity = math.max(0, capacity - reserved)
    local missing = math.max(0, available_capacity - tokens)
    local refill = 0

    if missing > 0 then
        -- Saturate before multiplication. This keeps the multiplication
        -- below the amount of quota actually missing.
        if refill_per_ms >= missing / elapsed_ms then
            refill = missing
        else
            refill = math.floor(elapsed_ms * refill_per_ms)
        end
    end

    tokens = math.min(available_capacity, tokens + refill)
    last_refill_ms = now_ms
end

-- Recover a bounded batch of expired active reservations.
local expired = redis.call(
    "ZRANGEBYSCORE",
    index_key,
    "-inf",
    now_ms,
    "LIMIT",
    0,
    cleanup_batch_size
)

for _, id in ipairs(expired) do
    local cost_field = id .. ":cost"
    local status_field = id .. ":status"

    local metadata = redis.call(
        "HMGET",
        data_key,
        cost_field,
        status_field
    )

    local expired_cost = tonumber(metadata[1])
    local status = metadata[2]

    if expired_cost ~= nil and status == "OPEN" then
        reserved = math.max(0, reserved - expired_cost)

        tokens = math.min(
            math.max(0, capacity - reserved),
            tokens + expired_cost
        )

        redis.call("HSET", data_key, status_field, "EXPIRED")
        redis.call(
            "ZADD",
            terminal_key,
            now_ms + retention_ms,
            id
        )
    end

    redis.call("ZREM", index_key, id)
end

-- Do not make a quota decision while expired reservations remain.
local more_expired = redis.call(
    "ZRANGEBYSCORE",
    index_key,
    "-inf",
    now_ms,
    "LIMIT",
    0,
    1
)

redis.call(
    "HSET",
    bucket_key,
    "tokens", tokens,
    "last_refill_ms", last_refill_ms,
    "reserved", reserved
)

if #more_expired > 0 then
    redis.call("PERSIST", bucket_key)
    redis.call("PERSIST", index_key)
    redis.call("PERSIST", data_key)
    redis.call("PERSIST", terminal_key)

    return {-1, math.floor(tokens), 0}
end

-- Reject requests that exceed currently available tokens.
if tokens < cost then
    local retry_ms = math.ceil((cost - tokens) / refill_per_ms)

    if retry_ms < 1 then
        retry_ms = 1
    end

    if redis.call("ZCARD", index_key) > 0 then
        redis.call("PERSIST", bucket_key)
        redis.call("PERSIST", index_key)
        redis.call("PERSIST", data_key)
        redis.call("PERSIST", terminal_key)
    else
        redis.call(
            "PEXPIRE",
            bucket_key,
            math.max(1000, math.ceil((capacity - tokens) / refill_per_ms))
        )
        redis.call("PEXPIRE", index_key, retention_ms)
        redis.call("PEXPIRE", data_key, retention_ms)
        redis.call("PEXPIRE", terminal_key, retention_ms)
    end

    return {0, tokens, retry_ms}
end

-- Reserve quota.
tokens = tokens - cost
reserved = reserved + cost

local expires_at_ms = now_ms + reservation_ttl_ms

redis.call(
    "HSET",
    bucket_key,
    "tokens", tokens,
    "last_refill_ms", last_refill_ms,
    "reserved", reserved
)

redis.call(
    "HSET",
    data_key,
    reservation_id .. ":cost", cost,
    reservation_id .. ":created_at_ms", now_ms,
    reservation_id .. ":expires_at_ms", expires_at_ms,
    reservation_id .. ":status", "OPEN"
)

redis.call("ZADD", index_key, expires_at_ms, reservation_id)

-- An active reservation must never lose its state or metadata to TTL.
redis.call("PERSIST", bucket_key)
redis.call("PERSIST", index_key)
redis.call("PERSIST", data_key)
redis.call("PERSIST", terminal_key)

return {1, tokens, 0}
`)

// Commit consumes reserved quota and moves metadata into terminal retention.
var commitTokenBucketReservationScript = redis.NewScript(`
local bucket_key = KEYS[1]
local data_key = KEYS[2]
local index_key = KEYS[3]
local terminal_key = KEYS[4]

local id = ARGV[1]
local capacity = tonumber(ARGV[2])
local refill_per_ms = tonumber(ARGV[3])

local status_field = id .. ":status"
local cost_field = id .. ":cost"
local expires_field = id .. ":expires_at_ms"

local status = redis.call("HGET", data_key, status_field)

if status == "COMMITTED" then
    return 1
end

if status == "ABORTED" or status == "EXPIRED" then
    return -1
end

if status ~= "OPEN" then
    return 0
end

local values = redis.call(
    "HMGET",
    data_key,
    cost_field,
    expires_field
)

local cost = tonumber(values[1])
local expires_at_ms = tonumber(values[2])

if cost == nil or expires_at_ms == nil then
    return 0
end

local now = redis.call("TIME")
local now_ms = tonumber(now[1]) * 1000
    + math.floor(tonumber(now[2]) / 1000)

-- An expired reservation must release its held quota exactly once.
if expires_at_ms <= now_ms then
    local bucket_values = redis.call(
        "HMGET",
        bucket_key,
        "tokens",
        "last_refill_ms",
        "reserved"
    )

    local tokens = tonumber(bucket_values[1])
    local last_refill_ms = tonumber(bucket_values[2])
    local reserved = tonumber(bucket_values[3]) or 0

    if tokens == nil then
        tokens = capacity
        last_refill_ms = now_ms
    end

    -- Refill before releasing the expired reservation.
    if now_ms > last_refill_ms then
        local elapsed_ms = now_ms - last_refill_ms
        local available_capacity = math.max(0, capacity - reserved)
        local missing = math.max(0, available_capacity - tokens)
        local refill = 0

        if missing > 0 then
            if refill_per_ms >= missing / elapsed_ms then
                refill = missing
            else
                refill = math.floor(elapsed_ms * refill_per_ms)
            end
        end

        tokens = math.min(available_capacity, tokens + refill)
        last_refill_ms = now_ms
    end

    reserved = math.max(0, reserved - cost)

    tokens = math.min(
        math.max(0, capacity - reserved),
        tokens + cost
    )

    redis.call(
        "HSET",
        bucket_key,
        "tokens", tokens,
        "last_refill_ms", last_refill_ms,
        "reserved", reserved
    )

    redis.call("HSET", data_key, status_field, "EXPIRED")
    redis.call("ZREM", index_key, id)
    redis.call("ZADD", terminal_key, now_ms + 3600000, id)

    if redis.call("ZCARD", index_key) > 0 then
        redis.call("PERSIST", bucket_key)
        redis.call("PERSIST", index_key)
        redis.call("PERSIST", data_key)
        redis.call("PERSIST", terminal_key)
    else
        local state_ttl = math.max(
            1000,
            math.ceil(math.max(0, capacity - tokens) / refill_per_ms)
        )

        redis.call("PEXPIRE", bucket_key, state_ttl)
        redis.call("PEXPIRE", index_key, 3600000)
        redis.call("PEXPIRE", data_key, 3600000)
        redis.call("PEXPIRE", terminal_key, 3600000)
    end

    return -1
end

-- Reservation is still open and has not expired. Commit it.
local reserved = tonumber(redis.call("HGET", bucket_key, "reserved")) or 0
reserved = math.max(0, reserved - cost)

redis.call("HSET", bucket_key, "reserved", reserved)
redis.call("HSET", data_key, status_field, "COMMITTED")
redis.call("ZREM", index_key, id)
redis.call("ZADD", terminal_key, now_ms + 3600000, id)

if redis.call("ZCARD", index_key) > 0 then
    redis.call("PERSIST", bucket_key)
    redis.call("PERSIST", index_key)
    redis.call("PERSIST", data_key)
    redis.call("PERSIST", terminal_key)
else
    local tokens = tonumber(redis.call("HGET", bucket_key, "tokens")) or 0
    local state_ttl = math.max(
        1000,
        math.ceil(math.max(0, capacity - tokens) / refill_per_ms)
    )

    redis.call("PEXPIRE", bucket_key, state_ttl)
    redis.call("PEXPIRE", index_key, 3600000)
    redis.call("PEXPIRE", data_key, 3600000)
    redis.call("PEXPIRE", terminal_key, 3600000)
end

return 1
`)

// Abort recovers expired reservations before restoring the requested reservation.
var abortTokenBucketReservationScript = redis.NewScript(`
local bucket_key = KEYS[1]
local data_key = KEYS[2]
local index_key = KEYS[3]
local terminal_key = KEYS[4]

local id = ARGV[1]
local capacity = tonumber(ARGV[2])
local refill_per_ms = tonumber(ARGV[3])

local status_field = id .. ":status"
local cost_field = id .. ":cost"
local expires_field = id .. ":expires_at_ms"

local initial_status = redis.call("HGET", data_key, status_field)

if initial_status == "ABORTED" then
    return 1
end

if initial_status == "COMMITTED" or initial_status == "EXPIRED" then
    return -1
end

if initial_status ~= "OPEN" then
    return 0
end

local now = redis.call("TIME")
local now_ms = tonumber(now[1]) * 1000
    + math.floor(tonumber(now[2]) / 1000)

-- Remove expired terminal metadata.
local terminal_expired = redis.call(
    "ZRANGEBYSCORE",
    terminal_key,
    "-inf",
    now_ms,
    "LIMIT",
    0,
    100
)

for _, terminal_id in ipairs(terminal_expired) do
    redis.call(
        "HDEL",
        data_key,
        terminal_id .. ":cost",
        terminal_id .. ":status",
        terminal_id .. ":created_at_ms",
        terminal_id .. ":expires_at_ms"
    )
    redis.call("ZREM", terminal_key, terminal_id)
end

-- Load and refill bucket state.
local values = redis.call(
    "HMGET",
    bucket_key,
    "tokens",
    "last_refill_ms",
    "reserved"
)

local tokens = tonumber(values[1])
local last_refill_ms = tonumber(values[2])
local reserved = tonumber(values[3]) or 0

if tokens == nil then
    tokens = capacity
    last_refill_ms = now_ms
end

if now_ms > last_refill_ms then
    local elapsed_ms = now_ms - last_refill_ms
    local available_capacity = math.max(0, capacity - reserved)
    local missing = math.max(0, available_capacity - tokens)
    local refill = 0

    if missing > 0 then
        -- Saturate before multiplication. This keeps the multiplication
        -- below the amount of quota actually missing.
        if refill_per_ms >= missing / elapsed_ms then
            refill = missing
        else
            refill = math.floor(elapsed_ms * refill_per_ms)
        end
    end

    tokens = math.min(available_capacity, tokens + refill)
    last_refill_ms = now_ms
end

-- Recover a bounded batch of expired reservations.
local expired = redis.call(
    "ZRANGEBYSCORE",
    index_key,
    "-inf",
    now_ms,
    "LIMIT",
    0,
    100
)

for _, expired_id in ipairs(expired) do
    local metadata = redis.call(
        "HMGET",
        data_key,
        expired_id .. ":cost",
        expired_id .. ":status"
    )

    local expired_cost = tonumber(metadata[1])
    local status = metadata[2]

    if expired_cost ~= nil and status == "OPEN" then
        reserved = math.max(0, reserved - expired_cost)

        tokens = math.min(
            math.max(0, capacity - reserved),
            tokens + expired_cost
        )

        redis.call(
            "HSET",
            data_key,
            expired_id .. ":status",
            "EXPIRED"
        )

        redis.call(
            "ZADD",
            terminal_key,
            now_ms + 3600000,
            expired_id
        )
    end

    redis.call("ZREM", index_key, expired_id)
end

local more_expired = redis.call(
    "ZRANGEBYSCORE",
    index_key,
    "-inf",
    now_ms,
    "LIMIT",
    0,
    1
)

redis.call(
    "HSET",
    bucket_key,
    "tokens", tokens,
    "last_refill_ms", last_refill_ms,
    "reserved", reserved
)

if #more_expired > 0 then
    redis.call("PERSIST", bucket_key)
    redis.call("PERSIST", index_key)
    redis.call("PERSIST", data_key)
    redis.call("PERSIST", terminal_key)
    return -2
end

-- The requested reservation may have been expired by the recovery loop.
local current_status = redis.call("HGET", data_key, status_field)

if current_status == "ABORTED" then
    return 1
end

if current_status == "COMMITTED" or current_status == "EXPIRED" then
    return -1
end

if current_status ~= "OPEN" then
    return 0
end

local reservation_values = redis.call(
    "HMGET",
    data_key,
    cost_field,
    expires_field
)

local cost = tonumber(reservation_values[1])
local expires_at_ms = tonumber(reservation_values[2])

-- Fail closed if metadata is incomplete or the reservation is expired.
if cost == nil or expires_at_ms == nil or expires_at_ms <= now_ms then
    return 0
end

-- Restore quota exactly once.
reserved = math.max(0, reserved - cost)

tokens = math.min(
    math.max(0, capacity - reserved),
    tokens + cost
)

redis.call(
    "HSET",
    bucket_key,
    "tokens", tokens,
    "last_refill_ms", last_refill_ms,
    "reserved", reserved
)

redis.call("HSET", data_key, status_field, "ABORTED")
redis.call("ZREM", index_key, id)
redis.call("ZADD", terminal_key, now_ms + 3600000, id)

if redis.call("ZCARD", index_key) > 0 then
    redis.call("PERSIST", bucket_key)
    redis.call("PERSIST", index_key)
    redis.call("PERSIST", data_key)
    redis.call("PERSIST", terminal_key)
else
    local state_ttl = math.max(
        1000,
        math.ceil(math.max(0, capacity - tokens) / refill_per_ms)
    )

    redis.call("PEXPIRE", bucket_key, state_ttl)
    redis.call("PEXPIRE", index_key, 3600000)
    redis.call("PEXPIRE", data_key, 3600000)
    redis.call("PEXPIRE", terminal_key, 3600000)
end

return 1
`)

func (l *TokenBucketReservationLimiter) Reserve(
	ctx context.Context,
	key string,
	capacity int64,
	refillRate float64,
	cost int64,
) (Decision, *TokenBucketReservation, error) {
	if l == nil || l.client == nil {
		return Decision{}, nil, errors.New("redis client is required")
	}
	if key == "" {
		return Decision{}, nil, errors.New("key is required")
	}
	if capacity <= 0 {
		return Decision{}, nil, errors.New("capacity must be greater than zero")
	}
	if refillRate <= 0 || math.IsNaN(refillRate) || math.IsInf(refillRate, 0) {
		return Decision{}, nil, errors.New("refill rate must be finite and greater than zero")
	}
	if cost <= 0 {
		return Decision{}, nil, errors.New("cost must be greater than zero")
	}
	if capacity > maxLuaExactInteger/reservationTokenScale ||
		cost > maxLuaExactInteger/reservationTokenScale {
		return Decision{}, nil, errors.New(
			"capacity or cost exceeds Redis Lua numeric precision",
		)
	}

	if cost > capacity {
		return Decision{
			Allowed:   false,
			Limit:     capacity,
			Remaining: capacity,
		}, nil, nil
	}

	capacityScaled := capacity * reservationTokenScale
	costScaled := cost * reservationTokenScale
	refillPerMillisecond := refillRate * float64(reservationTokenScale) / 1000

	if refillPerMillisecond <= 0 ||
		math.IsInf(refillPerMillisecond, 0) ||
		math.IsNaN(refillPerMillisecond) {
		return Decision{}, nil, errors.New("refill rate is too small or too large")
	}

	reservationID, err := nextReservationID()
	if err != nil {
		return Decision{}, nil, err
	}

	keys := tokenBucketKeysFor(key)

	var allowed int64
	var tokensScaled int64
	var retryAfterMs int64

	for attempt := 0; attempt < maxReservationCleanupAttempts; attempt++ {
		result, err := reserveTokenBucketScript.Run(
			ctx,
			l.client,
			[]string{
				keys.State,
				keys.Reservations,
				keys.ReservationData,
				keys.TerminalIndex,
			},
			capacityScaled,
			refillPerMillisecond,
			costScaled,
			reservationTTL.Milliseconds(),
			reservationID,
		).Result()
		if err != nil {
			return Decision{}, nil, err
		}

		values, ok := result.([]interface{})
		if !ok || len(values) != 3 {
			return Decision{}, nil, errors.New(
				"invalid token bucket reservation response",
			)
		}

		var valid bool

		allowed, valid = redisInt64TokenBucketReservation(values[0])
		if !valid {
			return Decision{}, nil, errors.New("invalid reservation allowed result")
		}

		tokensScaled, valid = redisInt64TokenBucketReservation(values[1])
		if !valid {
			return Decision{}, nil, errors.New("invalid reservation token result")
		}

		retryAfterMs, valid = redisInt64TokenBucketReservation(values[2])
		if !valid {
			return Decision{}, nil, errors.New("invalid reservation retry result")
		}

		if allowed != -1 {
			break
		}
	}

	if allowed == -1 {
		return Decision{}, nil, errors.New(
			"reservation cleanup backlog exceeds the per-request limit",
		)
	}

	remaining := tokensScaled / reservationTokenScale

	if allowed == 0 {
		return Decision{
			Allowed:    false,
			Limit:      capacity,
			Remaining:  remaining,
			RetryAfter: secondsCeil(retryAfterMs),
			ResetAfter: secondsCeil(int64(math.Ceil(
				float64(capacityScaled-tokensScaled) / refillPerMillisecond,
			))),
		}, nil, nil
	}

	reservation := &TokenBucketReservation{
		client:               l.client,
		key:                  keys.State,
		reservationDataKey:   keys.ReservationData,
		indexKey:             keys.Reservations,
		terminalIndexKey:     keys.TerminalIndex,
		reservationID:        reservationID,
		capacityScaled:       capacityScaled,
		refillPerMillisecond: refillPerMillisecond,
	}

	return Decision{
		Allowed:   true,
		Limit:     capacity,
		Remaining: remaining,
	}, reservation, nil
}

func (r *TokenBucketReservation) Commit(ctx context.Context) error {
	if r == nil || r.client == nil {
		return errors.New("token bucket reservation is required")
	}

	if !r.closed.CompareAndSwap(false, true) {
		return ErrReservationClosed
	}

	result, err := commitTokenBucketReservationScript.Run(
		ctx,
		r.client,
		[]string{
			r.key,
			r.reservationDataKey,
			r.indexKey,
			r.terminalIndexKey,
		},
		r.reservationID,
		r.capacityScaled,
		r.refillPerMillisecond,
	).Int64()
	if err != nil {
		r.closed.Store(false)
		return err
	}

	switch result {
	case 1:
		return nil
	case 0:
		r.closed.Store(false)
		return ErrReservationNotFound
	case -1:
		return ErrReservationClosed
	default:
		r.closed.Store(false)
		return fmt.Errorf("unexpected commit result: %d", result)
	}
}

func (r *TokenBucketReservation) Abort(ctx context.Context) error {
	if r == nil || r.client == nil {
		return errors.New("reservation is invalid")
	}

	if !r.closed.CompareAndSwap(false, true) {
		return ErrReservationClosed
	}

	var result int64
	var err error

	for attempt := 0; attempt < maxReservationCleanupAttempts; attempt++ {
		result, err = abortTokenBucketReservationScript.Run(
			ctx,
			r.client,
			[]string{
				r.key,
				r.reservationDataKey,
				r.indexKey,
				r.terminalIndexKey,
			},
			r.reservationID,
			r.capacityScaled,
			r.refillPerMillisecond,
		).Int64()

		if err != nil {
			r.closed.Store(false)
			return err
		}

		if result != -2 {
			break
		}
	}

	if result == -2 {
		r.closed.Store(false)
		return errors.New("reservation cleanup backlog exceeds the per-request limit")
	}

	switch result {
	case 1:
		return nil
	case 0:
		r.closed.Store(false)
		return ErrReservationNotFound
	case -1:
		return ErrReservationClosed
	default:
		r.closed.Store(false)
		return fmt.Errorf("unexpected abort result: %d", result)
	}
}

func nextReservationID() (string, error) {
	var randomBytes [16]byte

	if _, err := rand.Read(randomBytes[:]); err != nil {
		return "", fmt.Errorf("generate reservation ID: %w", err)
	}

	return hex.EncodeToString(randomBytes[:]), nil
}

func secondsCeil(milliseconds int64) int64 {
	if milliseconds <= 0 {
		return 0
	}
	return (milliseconds + 999) / 1000
}

func redisInt64TokenBucketReservation(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case uint64:
		if v > math.MaxInt64 {
			return 0, false
		}
		return int64(v), true
	default:
		return 0, false
	}
}
