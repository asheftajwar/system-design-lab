package limiter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"

	"github.com/redis/go-redis/v9"
)

const tokenScale int64 = 1_000_000

var (
	ErrInvalidRedisClient = errors.New("redis client must not be nil")
	ErrInvalidKey         = errors.New("rate limit key must not be empty")
)

type tokenBucketKeys struct {
	State           string
	Reservations    string
	ReservationData string
	TerminalIndex   string
}

func redisClusterKeyPrefix(key string) string {
	sum := sha256.Sum256([]byte(key))
	hashTag := hex.EncodeToString(sum[:])

	return "rl:{" + hashTag + "}"
}

func tokenBucketKeysFor(key string) tokenBucketKeys {
	prefix := redisClusterKeyPrefix(key)

	return tokenBucketKeys{
		State:           prefix + ":state",
		Reservations:    prefix + ":reservations",
		ReservationData: prefix + ":reservation-data",
		TerminalIndex:   prefix + ":terminal-index",
	}
}

type RedisTokenBucket struct {
	client redis.Scripter
}

func NewRedisTokenBucket(client redis.Scripter) (*RedisTokenBucket, error) {
	if client == nil {
		return nil, ErrInvalidRedisClient
	}

	return &RedisTokenBucket{
		client: client,
	}, nil
}

var tokenBucketScript = redis.NewScript(`
local state_key = KEYS[1]
local reservations_key = KEYS[2]
local reservation_data_key = KEYS[3]
local terminal_index_key = KEYS[4]

local redis_time = redis.call("TIME")
local now_ms =
    (tonumber(redis_time[1]) * 1000) +
    math.floor(tonumber(redis_time[2]) / 1000)

local capacity = tonumber(ARGV[1])
local refill_per_second = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local ttl_ms = tonumber(ARGV[4])

local cleanup_batch_size = 100
local terminal_retention_ms = 3600000

-- Remove terminal reservation metadata whose retention period has ended.
local expired_terminal_ids = redis.call(
    "ZRANGEBYSCORE",
    terminal_index_key,
    "-inf",
    now_ms,
    "LIMIT",
    0,
    cleanup_batch_size
)

for _, reservation_id in ipairs(expired_terminal_ids) do
    redis.call(
        "HDEL",
        reservation_data_key,
        reservation_id .. ":cost",
        reservation_id .. ":status"
    )

    redis.call("ZREM", terminal_index_key, reservation_id)
end

-- Load bucket state.
local state = redis.call(
    "HMGET",
    state_key,
    "tokens",
    "last_refill_ms",
    "reserved"
)

local tokens = tonumber(state[1])
local last_refill_ms = tonumber(state[2])
local reserved = tonumber(state[3]) or 0

if tokens == nil then
    tokens = capacity
    last_refill_ms = now_ms
else
    local elapsed_ms = now_ms - last_refill_ms

    if elapsed_ms > 0 then
        local refill = math.floor(
            (elapsed_ms * refill_per_second) / 1000
        )

        local available_capacity = math.max(0, capacity - reserved)

        tokens = math.min(
            available_capacity,
            tokens + refill
        )

        last_refill_ms = now_ms
    end
end

-- Recover a bounded batch of expired active reservations.
local expired_ids = redis.call(
    "ZRANGEBYSCORE",
    reservations_key,
    "-inf",
    now_ms,
    "LIMIT",
    0,
    cleanup_batch_size
)

for _, reservation_id in ipairs(expired_ids) do
    local cost_field = reservation_id .. ":cost"
    local status_field = reservation_id .. ":status"

    local values = redis.call(
        "HMGET",
        reservation_data_key,
        cost_field,
        status_field
    )

    local expired_cost = tonumber(values[1])
    local status = values[2]

    if expired_cost ~= nil and status == "OPEN" then
        reserved = math.max(0, reserved - expired_cost)

        tokens = math.min(
            math.max(0, capacity - reserved),
            tokens + expired_cost
        )

        redis.call(
            "HSET",
            reservation_data_key,
            status_field,
            "EXPIRED"
        )

        redis.call(
            "ZADD",
            terminal_index_key,
            now_ms + terminal_retention_ms,
            reservation_id
        )
    end

    redis.call("ZREM", reservations_key, reservation_id)
end

-- Do not make a decision while expired reservations remain.
local more_expired = redis.call(
    "ZRANGEBYSCORE",
    reservations_key,
    "-inf",
    now_ms,
    "LIMIT",
    0,
    1
)

redis.call(
    "HSET",
    state_key,
    "tokens", tokens,
    "last_refill_ms", last_refill_ms,
    "reserved", reserved
)

if #more_expired > 0 then
    redis.call("PERSIST", state_key)
    redis.call("PERSIST", reservations_key)
    redis.call("PERSIST", reservation_data_key)
    redis.call("PERSIST", terminal_index_key)

    return {-1, math.floor(tokens), 0}
end

-- Make the allow/deny decision.
local allowed = 0
local retry_after_ms = 0

if cost <= capacity and tokens >= cost then
    tokens = tokens - cost
    allowed = 1
elseif cost <= capacity then
    local missing = cost - tokens

    retry_after_ms = math.ceil(
        (missing * 1000) / refill_per_second
    )

    if retry_after_ms < 1 then
        retry_after_ms = 1
    end
end

redis.call(
    "HSET",
    state_key,
    "tokens", tokens,
    "last_refill_ms", last_refill_ms,
    "reserved", reserved
)

-- Keep the bucket and reservation metadata alive while reservations exist.
if redis.call("ZCARD", reservations_key) > 0 then
    redis.call("PERSIST", state_key)
    redis.call("PERSIST", reservations_key)
    redis.call("PERSIST", reservation_data_key)
    redis.call("PERSIST", terminal_index_key)
else
    redis.call("PEXPIRE", state_key, ttl_ms)
    redis.call("PEXPIRE", reservations_key, ttl_ms)
    redis.call("PEXPIRE", reservation_data_key, terminal_retention_ms)
    redis.call("PEXPIRE", terminal_index_key, terminal_retention_ms)
end

return {
    allowed,
    math.floor(tokens),
    retry_after_ms
}
`)

func (b *RedisTokenBucket) Allow(
	ctx context.Context,
	key string,
	capacity int64,
	refillRate float64,
	cost int64,
) (Decision, error) {
	if key == "" {
		return Decision{}, ErrInvalidKey
	}

	if capacity <= 0 {
		return Decision{}, ErrInvalidCapacity
	}

	if refillRate <= 0 {
		return Decision{}, ErrInvalidRefillRate
	}

	if cost <= 0 {
		return Decision{}, ErrInvalidCost
	}

	if math.IsNaN(refillRate) || math.IsInf(refillRate, 0) {
		return Decision{}, ErrInvalidRefillRate
	}

	maxScaledValue := int64(^uint64(0) >> 1)

	if capacity > maxScaledValue/tokenScale {
		return Decision{}, ErrInvalidCapacity
	}

	if cost > maxScaledValue/tokenScale {
		return Decision{}, ErrInvalidCost
	}

	maxRefillRate := float64(maxScaledValue) / float64(tokenScale)

	if refillRate > maxRefillRate {
		return Decision{}, ErrInvalidRefillRate
	}

	capacityScaled := capacity * tokenScale
	costScaled := cost * tokenScale
	refillScaled := int64(math.Round(refillRate * float64(tokenScale)))

	if refillScaled <= 0 {
		return Decision{}, ErrInvalidRefillRate
	}

	ttlMS := int64(math.Ceil(
		float64(capacityScaled) / float64(refillScaled) * 1000,
	))

	if ttlMS < 1000 {
		ttlMS = 1000
	}

	var values []interface{}

	const maxCleanupAttempts = 10

	for attempt := 0; attempt < maxCleanupAttempts; attempt++ {
		keys := tokenBucketKeysFor(key)

		result, err := tokenBucketScript.Run(
			ctx,
			b.client,
			[]string{
				keys.State,
				keys.Reservations,
				keys.ReservationData,
				keys.TerminalIndex,
			},
			capacityScaled,
			refillScaled,
			costScaled,
			ttlMS,
		).Result()

		if err != nil {
			return Decision{}, fmt.Errorf("execute token bucket script: %w", err)
		}

		var ok bool
		values, ok = result.([]interface{})
		if !ok || len(values) != 3 {
			return Decision{}, fmt.Errorf("unexpected redis response: %T", result)
		}

		allowed, err := redisInt64(values[0])
		if err != nil {
			return Decision{}, err
		}

		if allowed == -1 {
			// Expired entries remain. Run another bounded cleanup batch.
			continue
		}

		// A normal allow/deny decision is ready for response parsing.
		break
	}

	if len(values) != 3 {
		return Decision{}, errors.New("invalid token bucket response")
	}

	allowed, err := redisInt64(values[0])
	if err != nil {
		return Decision{}, err
	}
	if allowed == -1 {
		return Decision{}, errors.New("reservation cleanup backlog exceeds the per-request limit")
	}

	remainingScaled, err := redisInt64(values[1])
	if err != nil {
		return Decision{}, err
	}

	remaining := remainingScaled / tokenScale

	retryAfterMS, err := redisInt64(values[2])
	if err != nil {
		return Decision{}, err
	}

	retryAfter := int64(0)

	if retryAfterMS > 0 {
		retryAfter = int64(math.Ceil(float64(retryAfterMS) / 1000))

		if retryAfter < 1 {
			retryAfter = 1
		}
	}

	resetAfter := int64(0)

	missingForFull := capacityScaled - remainingScaled
	if missingForFull > 0 {
		resetAfter = int64(math.Ceil(
			float64(missingForFull) / float64(refillScaled),
		))

		if resetAfter < 1 {
			resetAfter = 1
		}
	}

	return Decision{
		Allowed:    allowed == 1,
		Limit:      capacity,
		Remaining:  remaining,
		RetryAfter: retryAfter,
		ResetAfter: resetAfter,
	}, nil
}

func redisInt64(value interface{}) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case string:
		var result int64

		_, err := fmt.Sscanf(v, "%d", &result)

		return result, err
	default:
		return 0, fmt.Errorf("unexpected redis integer type %T", value)
	}
}
