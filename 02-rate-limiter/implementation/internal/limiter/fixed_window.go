package limiter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrInvalidFixedWindowLimit  = errors.New("fixed-window limit must be greater than zero")
	ErrInvalidFixedWindowWindow = errors.New("fixed-window duration must be greater than zero")
	ErrInvalidFixedWindowCost   = errors.New("fixed-window request cost must be greater than zero")
)

const fixedWindowMinTTL = time.Second

var fixedWindowScript = redis.NewScript(`
local key = KEYS[1]

local limit = tonumber(ARGV[1])
local cost = tonumber(ARGV[2])
local window_ms = tonumber(ARGV[3])

local now = redis.call("TIME")
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)

local window_id = math.floor(now_ms / window_ms)
local window_start_ms = window_id * window_ms
local reset_ms = window_start_ms + window_ms - now_ms

if reset_ms < 1 then
    reset_ms = 1
end

local raw = redis.call("HMGET", key, "window_id", "count")

local current_window = tonumber(raw[1])
local current_count = tonumber(raw[2])

if current_window == nil or current_window ~= window_id then
    if cost > limit then
        return {0, limit, reset_ms}
    end

    redis.call("HSET", key,
        "window_id", window_id,
        "count", cost
    )

    local ttl_ms = reset_ms
    if ttl_ms < 1000 then
        ttl_ms = 1000
    end

    redis.call("PEXPIRE", key, ttl_ms)

    return {1, limit - cost, reset_ms}
end

if current_count + cost > limit then
    return {0, limit - current_count, reset_ms}
end

local new_count = current_count + cost

redis.call("HSET", key, "count", new_count)

local ttl_ms = reset_ms
if ttl_ms < 1000 then
    ttl_ms = 1000
end

redis.call("PEXPIRE", key, ttl_ms)

return {1, limit - new_count, reset_ms}
`)

type RedisFixedWindow struct {
	client *redis.Client
}

func NewRedisFixedWindow(client *redis.Client) *RedisFixedWindow {
	return &RedisFixedWindow{
		client: client,
	}
}

func (w *RedisFixedWindow) Allow(
	ctx context.Context,
	key string,
	limit int64,
	windowSeconds int64,
	cost int64,
) (Decision, error) {
	if w == nil || w.client == nil {
		return Decision{}, errors.New("redis fixed-window client is nil")
	}

	if key == "" {
		return Decision{}, errors.New("fixed-window key is required")
	}

	if limit <= 0 {
		return Decision{}, ErrInvalidFixedWindowLimit
	}

	if windowSeconds <= 0 {
		return Decision{}, ErrInvalidFixedWindowWindow
	}

	if cost <= 0 {
		return Decision{}, ErrInvalidFixedWindowCost
	}

	if windowSeconds > math.MaxInt64/1000 {
		return Decision{}, errors.New("fixed-window duration overflows milliseconds")
	}

	windowMS := windowSeconds * 1000

	result, err := fixedWindowScript.Run(
		ctx,
		w.client,
		[]string{key + ":fw"},
		limit,
		cost,
		windowMS,
	).Result()
	if err != nil {
		return Decision{}, fmt.Errorf("execute fixed-window script: %w", err)
	}

	values, ok := result.([]interface{})
	if !ok || len(values) != 3 {
		return Decision{}, errors.New("invalid fixed-window redis response")
	}

	allowed, err := redisInt64(values[0])
	if err != nil {
		return Decision{}, fmt.Errorf("parse fixed-window allowed result: %w", err)
	}

	remaining, err := redisInt64(values[1])
	if err != nil {
		return Decision{}, fmt.Errorf("parse fixed-window remaining result: %w", err)
	}

	resetMS, err := redisInt64(values[2])
	if err != nil {
		return Decision{}, fmt.Errorf("parse fixed-window reset result: %w", err)
	}

	resetAfter := ceilSeconds(resetMS)

	return Decision{
		Allowed:    allowed == 1,
		Limit:      limit,
		Remaining:  maxInt64(remaining, 0),
		RetryAfter: resetAfter,
		ResetAfter: resetAfter,
	}, nil
}

// Already declared in redis_token_bucket.go
// func redisInt64(value interface{}) (int64, error) {
// 	switch v := value.(type) {
// 	case int64:
// 		return v, nil
// 	case string:
// 		return strconv.ParseInt(v, 10, 64)
// 	case []byte:
// 		return strconv.ParseInt(string(v), 10, 64)
// 	default:
// 		return 0, fmt.Errorf("unexpected redis integer type %T", value)
// 	}
// }

func ceilSeconds(milliseconds int64) int64 {
	if milliseconds <= 0 {
		return 0
	}

	return (milliseconds + 999) / 1000
}

func maxInt64(value, minimum int64) int64 {
	if value < minimum {
		return minimum
	}

	return value
}
