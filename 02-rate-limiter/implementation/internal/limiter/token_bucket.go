package limiter

import (
	"errors"
	"math"
	"time"
)

var (
	ErrInvalidCapacity   = errors.New("capacity must be greater than zero")
	ErrInvalidRefillRate = errors.New("refill rate must be greater than zero")
	ErrInvalidCost       = errors.New("request cost must be greater than zero")
)

type TokenBucket struct {
	capacity   float64
	refillRate float64
	tokens     float64
	lastRefill time.Time
}

func NewTokenBucket(
	capacity int64,
	refillRate float64,
	now time.Time,
) (*TokenBucket, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}

	if refillRate <= 0 {
		return nil, ErrInvalidRefillRate
	}

	return &TokenBucket{
		capacity:   float64(capacity),
		refillRate: refillRate,
		tokens:     float64(capacity),
		lastRefill: now,
	}, nil
}

func (b *TokenBucket) Allow(
	now time.Time,
	cost int64,
) (Decision, error) {
	if cost <= 0 {
		return Decision{}, ErrInvalidCost
	}

	b.refill(now)

	requestCost := float64(cost)

	if requestCost > b.capacity {
		return Decision{
			Allowed:    false,
			Limit:      int64(b.capacity),
			Remaining:  int64(math.Floor(b.tokens)),
			RetryAfter: 0,
			ResetAfter: 0,
		}, nil
	}

	if b.tokens >= requestCost {
		b.tokens -= requestCost

		return Decision{
			Allowed:    true,
			Limit:      int64(b.capacity),
			Remaining:  int64(math.Floor(b.tokens)),
			RetryAfter: 0,
			ResetAfter: b.resetAfter(now),
		}, nil
	}

	missing := requestCost - b.tokens
	retryAfter := int64(math.Ceil(missing / b.refillRate))

	if retryAfter < 1 {
		retryAfter = 1
	}

	return Decision{
		Allowed:    false,
		Limit:      int64(b.capacity),
		Remaining:  int64(math.Floor(b.tokens)),
		RetryAfter: retryAfter,
		ResetAfter: retryAfter,
	}, nil
}

func (b *TokenBucket) refill(now time.Time) {
	if !now.After(b.lastRefill) {
		return
	}

	elapsed := now.Sub(b.lastRefill).Seconds()

	b.tokens += elapsed * b.refillRate

	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}

	b.lastRefill = now
}

func (b *TokenBucket) resetAfter(now time.Time) int64 {
	missing := b.capacity - b.tokens

	if missing <= 0 {
		return 0
	}

	seconds := math.Ceil(missing / b.refillRate)

	if seconds < 1 {
		return 1
	}

	return int64(seconds)
}
