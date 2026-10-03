package limiter

import (
	"context"
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

type RedisTokenBucket struct {
	client *redis.Client
}

func NewRedisTokenBucket(client *redis.Client) (*RedisTokenBucket, error) {
	if client == nil {
		return nil, ErrInvalidRedisClient
	}

	return &RedisTokenBucket{
		client: client,
	}, nil
}

var tokenBucketScript = redis.NewScript(`
local key = KEYS[1]

local redis_time = redis.call("TIME")
local now_ms =
    (tonumber(redis_time[1]) * 1000) +
    math.floor(tonumber(redis_time[2]) / 1000)

local capacity = tonumber(ARGV[1])
local refill_per_second = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local ttl_ms = tonumber(ARGV[4])

local state = redis.call("HMGET", key, "tokens", "last_refill_ms")

local tokens = tonumber(state[1])
local last_refill_ms = tonumber(state[2])

if tokens == nil then
    tokens = capacity
    last_refill_ms = now_ms
else
    local elapsed_ms = now_ms - last_refill_ms

    if elapsed_ms > 0 then
        local refill = math.floor(
            (elapsed_ms * refill_per_second) / 1000
        )

        tokens = math.min(capacity, tokens + refill)
        last_refill_ms = now_ms
    end
end

local allowed = 0
local retry_after_ms = 0

if cost <= capacity and tokens >= cost then
    tokens = tokens - cost
    allowed = 1
else
    if cost > capacity then
        retry_after_ms = 0
    else
        local missing = cost - tokens

        retry_after_ms = math.ceil(
            (missing * 1000) / refill_per_second
        )

        if retry_after_ms < 1 then
            retry_after_ms = 1
        end
    end
end

redis.call(
    "HSET",
    key,
    "tokens",
    tokens,
    "last_refill_ms",
    last_refill_ms
)

redis.call("PEXPIRE", key, ttl_ms)

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

	result, err := tokenBucketScript.Run(
		ctx,
		b.client,
		[]string{key},
		capacityScaled,
		refillScaled,
		costScaled,
		ttlMS,
	).Result()

	if err != nil {
		return Decision{}, fmt.Errorf("execute token bucket script: %w", err)
	}

	values, ok := result.([]interface{})
	if !ok || len(values) != 3 {
		return Decision{}, fmt.Errorf("unexpected redis response: %T", result)
	}

	allowed, err := redisInt64(values[0])
	if err != nil {
		return Decision{}, err
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
