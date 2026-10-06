package limiter

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/redis/go-redis/v9"
)

var (
	ErrInvalidSlidingWindowLimit  = errors.New("sliding-window limit must be greater than zero")
	ErrInvalidSlidingWindowWindow = errors.New("sliding-window duration must be greater than zero")
	ErrInvalidSlidingWindowCost   = errors.New("sliding-window request cost must be greater than zero")
)

const slidingWindowScript = `
local zset_key = KEYS[1]
local cost_key = KEYS[2]
local usage_key = KEYS[3]
local seq_key = KEYS[4]

local limit = tonumber(ARGV[1])
local cost = tonumber(ARGV[2])
local window_ms = tonumber(ARGV[3])

local now = redis.call("TIME")
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)

local cutoff_ms = now_ms - window_ms

local expired = redis.call(
	"ZRANGEBYSCORE",
	zset_key,
	"-inf",
	cutoff_ms
)

local usage = tonumber(redis.call("GET", usage_key) or "0")

for _, member in ipairs(expired) do
	local member_cost = tonumber(redis.call("HGET", cost_key, member) or "0")

	usage = usage - member_cost

	redis.call("HDEL", cost_key, member)
	redis.call("ZREM", zset_key, member)
end

if usage < 0 then
	usage = 0
end

-- A request whose cost exceeds the entire configured limit
-- can never be admitted.
if cost > limit then
	if usage == 0 then
		redis.call("DEL", zset_key, cost_key, usage_key, seq_key)
	else
		redis.call("SET", usage_key, usage)
	end

	return {
		0,
		limit - usage,
		0,
		0
	}
end

-- Fast path: enough capacity is currently available.
if usage + cost <= limit then
	local sequence = redis.call("INCR", seq_key)
	local request_id = tostring(now_ms) .. "-" .. tostring(sequence)

	redis.call(
		"ZADD",
		zset_key,
		now_ms,
		request_id
	)

	redis.call(
		"HSET",
		cost_key,
		request_id,
		cost
	)

	usage = usage + cost

	redis.call("SET", usage_key, usage)

	local reset_after_ms = window_ms

	local oldest = redis.call(
		"ZRANGE",
		zset_key,
		0,
		0,
		"WITHSCORES"
	)

	if #oldest >= 2 then
		local oldest_ms = tonumber(oldest[2])

		reset_after_ms = oldest_ms + window_ms - now_ms

		if reset_after_ms < 0 then
			reset_after_ms = 0
		end
	end

	local ttl_ms = reset_after_ms

	if ttl_ms < 1000 then
		ttl_ms = 1000
	end

	redis.call("PEXPIRE", zset_key, ttl_ms)
	redis.call("PEXPIRE", cost_key, ttl_ms)
	redis.call("PEXPIRE", usage_key, ttl_ms)
	redis.call("PEXPIRE", seq_key, ttl_ms)

	return {
		1,
		limit - usage,
		0,
		reset_after_ms
	}
end

-- Not enough capacity. Find the earliest point at which
-- enough existing cost expires to admit this request.
local required_release = usage + cost - limit
local released = 0
local retry_after_ms = window_ms

local entries = redis.call(
	"ZRANGE",
	zset_key,
	0,
	-1,
	"WITHSCORES"
)

for index = 1, #entries, 2 do
	local member = entries[index]
	local member_ms = tonumber(entries[index + 1])
	local member_cost = tonumber(redis.call("HGET", cost_key, member) or "0")

	released = released + member_cost

	retry_after_ms = member_ms + window_ms - now_ms

	if retry_after_ms < 0 then
		retry_after_ms = 0
	end

	if released >= required_release then
		break
	end
end

redis.call("SET", usage_key, usage)

local ttl_ms = retry_after_ms

if ttl_ms < 1000 then
	ttl_ms = 1000
end

redis.call("PEXPIRE", zset_key, ttl_ms)
redis.call("PEXPIRE", cost_key, ttl_ms)
redis.call("PEXPIRE", usage_key, ttl_ms)
redis.call("PEXPIRE", seq_key, ttl_ms)

return {
	0,
	limit - usage,
	retry_after_ms,
	retry_after_ms
}
`

type RedisSlidingWindow struct {
	client *redis.Client
}

func NewRedisSlidingWindow(client *redis.Client) *RedisSlidingWindow {
	return &RedisSlidingWindow{
		client: client,
	}
}

func (w *RedisSlidingWindow) Allow(
	ctx context.Context,
	key string,
	limit int64,
	windowSeconds int64,
	cost int64,
) (Decision, error) {
	if limit <= 0 {
		return Decision{}, ErrInvalidSlidingWindowLimit
	}

	if windowSeconds <= 0 {
		return Decision{}, ErrInvalidSlidingWindowWindow
	}

	if cost <= 0 {
		return Decision{}, ErrInvalidSlidingWindowCost
	}

	const millisecondsPerSecond int64 = 1000

	if windowSeconds > math.MaxInt64/millisecondsPerSecond {
		return Decision{}, ErrInvalidSlidingWindowWindow
	}

	windowMS := windowSeconds * millisecondsPerSecond

	zsetKey := key + ":swl:z"
	costKey := key + ":swl:cost"
	usageKey := key + ":swl:usage"
	seqKey := key + ":swl:seq"

	result, err := redis.NewScript(slidingWindowScript).Run(
		ctx,
		w.client,
		[]string{
			zsetKey,
			costKey,
			usageKey,
			seqKey,
		},
		limit,
		cost,
		windowMS,
	).Result()
	if err != nil {
		return Decision{}, err
	}

	values, ok := result.([]interface{})
	if !ok || len(values) != 4 {
		return Decision{}, fmt.Errorf(
			"unexpected sliding-window Redis response: %T",
			result,
		)
	}

	allowed, err := redisInt64(values[0])
	if err != nil {
		return Decision{}, err
	}

	remaining, err := redisInt64(values[1])
	if err != nil {
		return Decision{}, err
	}

	retryAfterMS, err := redisInt64(values[2])
	if err != nil {
		return Decision{}, err
	}

	resetAfterMS, err := redisInt64(values[3])
	if err != nil {
		return Decision{}, err
	}

	return Decision{
		Allowed:    allowed == 1,
		Limit:      limit,
		Remaining:  remaining,
		RetryAfter: ceilSeconds(retryAfterMS),
		ResetAfter: ceilSeconds(resetAfterMS),
	}, nil
}

func ceilSeconds(milliseconds int64) int64 {
	if milliseconds <= 0 {
		return 0
	}

	return int64(math.Ceil(float64(milliseconds) / 1000))
}
