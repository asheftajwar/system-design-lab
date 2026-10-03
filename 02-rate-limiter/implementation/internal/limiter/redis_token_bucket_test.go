package limiter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()

	client := redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:6379",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()

		t.Skipf("Redis unavailable: %v", err)
	}

	t.Cleanup(func() {
		client.Close()
	})

	return client
}

func TestRedisTokenBucket_AllowsWithinCapacity(t *testing.T) {
	client := newTestRedis(t)

	ctx := context.Background()

	key := testRedisKey(t, "capacity")

	bucket, err := NewRedisTokenBucket(client)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		decision, err := bucket.Allow(
			ctx,
			key,
			5,
			1,
			1,
		)

		if err != nil {
			t.Fatal(err)
		}

		if !decision.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}
}

func TestRedisTokenBucket_RejectsAfterCapacity(t *testing.T) {
	client := newTestRedis(t)

	ctx := context.Background()

	key := testRedisKey(t, "reject")

	bucket, err := NewRedisTokenBucket(client)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		_, err := bucket.Allow(
			ctx,
			key,
			5,
			1,
			1,
		)

		if err != nil {
			t.Fatal(err)
		}
	}

	decision, err := bucket.Allow(
		ctx,
		key,
		5,
		1,
		1,
	)

	if err != nil {
		t.Fatal(err)
	}

	if decision.Allowed {
		t.Fatal("sixth request should be rejected")
	}

	if decision.RetryAfter != 1 {
		t.Fatalf(
			"expected RetryAfter=1, got %d",
			decision.RetryAfter,
		)
	}
}

func TestRedisTokenBucket_Refills(t *testing.T) {
	client := newTestRedis(t)

	ctx := context.Background()

	key := testRedisKey(t, "refill")

	bucket, err := NewRedisTokenBucket(client)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		_, err := bucket.Allow(
			ctx,
			key,
			5,
			1,
			1,
		)

		if err != nil {
			t.Fatal(err)
		}
	}

	time.Sleep(1100 * time.Millisecond)

	decision, err := bucket.Allow(
		ctx,
		key,
		5,
		1,
		1,
	)

	if err != nil {
		t.Fatal(err)
	}

	if !decision.Allowed {
		t.Fatal("request should be allowed after refill")
	}
}

func TestRedisTokenBucket_RequestCost(t *testing.T) {
	client := newTestRedis(t)

	ctx := context.Background()

	key := testRedisKey(t, "cost")

	bucket, err := NewRedisTokenBucket(client)
	if err != nil {
		t.Fatal(err)
	}

	decision, err := bucket.Allow(
		ctx,
		key,
		10,
		1,
		7,
	)

	if err != nil {
		t.Fatal(err)
	}

	if !decision.Allowed {
		t.Fatal("request should be allowed")
	}

	if decision.Remaining != 3 {
		t.Fatalf(
			"expected remaining=3, got %d",
			decision.Remaining,
		)
	}
}

func TestRedisTokenBucket_ConcurrentRequestsCannotOversubscribe(t *testing.T) {
	client := newTestRedis(t)

	ctx := context.Background()

	key := testRedisKey(t, "concurrency")

	bucket, err := NewRedisTokenBucket(client)
	if err != nil {
		t.Fatal(err)
	}

	const (
		capacity    = int64(100)
		concurrency = 500
	)

	var wg sync.WaitGroup

	var mu sync.Mutex

	allowed := 0

	for i := 0; i < concurrency; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			decision, err := bucket.Allow(
				ctx,
				key,
				capacity,
				1,
				1,
			)

			if err != nil {
				t.Errorf("Allow failed: %v", err)
				return
			}

			if decision.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if allowed != int(capacity) {
		t.Fatalf(
			"expected exactly %d allowed requests, got %d",
			capacity,
			allowed,
		)
	}
}

func TestRedisTokenBucket_SharedInstancesShareState(t *testing.T) {
	client := newTestRedis(t)

	limiterA, err := NewRedisTokenBucket(client)
	if err != nil {
		t.Fatalf("create limiter A: %v", err)
	}

	limiterB, err := NewRedisTokenBucket(client)
	if err != nil {
		t.Fatalf("create limiter B: %v", err)
	}

	key := fmt.Sprintf("test:shared:%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = client.Del(context.Background(), key).Err()
	})

	decision, err := limiterA.Allow(
		context.Background(),
		key,
		5,
		1,
		3,
	)
	if err != nil {
		t.Fatalf("limiter A allow: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("limiter A request should be allowed")
	}

	decision, err = limiterB.Allow(
		context.Background(),
		key,
		5,
		1,
		2,
	)
	if err != nil {
		t.Fatalf("limiter B allow: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("limiter B request should be allowed")
	}

	decision, err = limiterA.Allow(
		context.Background(),
		key,
		5,
		1,
		1,
	)
	if err != nil {
		t.Fatalf("final allow: %v", err)
	}

	if decision.Allowed {
		t.Fatal("request should be rejected after shared bucket is exhausted")
	}
}

func TestRedisTokenBucket_FractionalRefillRate(t *testing.T) {
	client := newTestRedis(t)

	limiter, err := NewRedisTokenBucket(client)
	if err != nil {
		t.Fatalf("create limiter: %v", err)
	}

	key := fmt.Sprintf("test:fractional:%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = client.Del(context.Background(), key).Err()
	})

	decision, err := limiter.Allow(
		context.Background(),
		key,
		5,
		2.5,
		5,
	)
	if err != nil {
		t.Fatalf("consume initial capacity: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("initial request should be allowed")
	}

	time.Sleep(450 * time.Millisecond)

	decision, err = limiter.Allow(
		context.Background(),
		key,
		5,
		2.5,
		1,
	)

	if err != nil {
		t.Fatalf("fractional refill request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("one token should have refilled after 400ms at 2.5 tokens/sec")
	}
}

func TestRedisTokenBucket_RejectedRequestDoesNotConsumeTokens(t *testing.T) {
	client := newTestRedis(t)

	limiter, err := NewRedisTokenBucket(client)
	if err != nil {
		t.Fatalf("create limiter: %v", err)
	}

	key := fmt.Sprintf("test:rejection:%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = client.Del(context.Background(), key).Err()
	})

	// Consume 6 of 10 tokens.
	decision, err := limiter.Allow(
		context.Background(),
		key,
		10,
		1,
		6,
	)
	if err != nil {
		t.Fatalf("initial request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("initial request should be allowed")
	}

	// Only 4 tokens remain, so cost 5 must be rejected.
	decision, err = limiter.Allow(
		context.Background(),
		key,
		10,
		1,
		5,
	)
	if err != nil {
		t.Fatalf("rejected request: %v", err)
	}

	if decision.Allowed {
		t.Fatal("request should be rejected")
	}

	// If the rejected request consumed tokens, this would fail.
	decision, err = limiter.Allow(
		context.Background(),
		key,
		10,
		1,
		4,
	)
	if err != nil {
		t.Fatalf("final request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("remaining 4 tokens should still be available")
	}
}

func TestRedisTokenBucket_SetsExpiration(t *testing.T) {
	client := newTestRedis(t)

	limiter, err := NewRedisTokenBucket(client)
	if err != nil {
		t.Fatalf("create limiter: %v", err)
	}

	key := fmt.Sprintf("test:ttl:%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = client.Del(context.Background(), key).Err()
	})

	_, err = limiter.Allow(
		context.Background(),
		key,
		10,
		1,
		1,
	)
	if err != nil {
		t.Fatalf("allow request: %v", err)
	}

	ttl, err := client.PTTL(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("read TTL: %v", err)
	}

	if ttl <= 0 {
		t.Fatal("rate-limit state should have a positive TTL")
	}

	if ttl > 10*time.Second {
		t.Fatalf("unexpectedly long TTL: %v", ttl)
	}
}

func testRedisKey(t *testing.T, name string) string {
	t.Helper()

	return fmt.Sprintf(
		"test:rate-limit:%s:%d",
		name,
		time.Now().UnixNano(),
	)
}

func TestRedisTokenBucket_ResetAfterMeansTimeUntilFull(t *testing.T) {
	client := newTestRedis(t)

	limiter, err := NewRedisTokenBucket(client)
	if err != nil {
		t.Fatalf("create limiter: %v", err)
	}

	key := testRedisKey(t, "reset-after")

	decision, err := limiter.Allow(
		context.Background(),
		key,
		10,
		1,
		4,
	)
	if err != nil {
		t.Fatalf("allow request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("request should be allowed")
	}

	if decision.Remaining != 6 {
		t.Fatalf(
			"expected remaining=6, got %d",
			decision.Remaining,
		)
	}

	// Approximately four seconds are required to refill
	// the four tokens that were consumed.
	if decision.ResetAfter < 3 || decision.ResetAfter > 5 {
		t.Fatalf(
			"expected ResetAfter around 4 seconds, got %d",
			decision.ResetAfter,
		)
	}
}

func TestRedisTokenBucket_RejectsOverflowConfiguration(t *testing.T) {
	client := newTestRedis(t)

	limiter, err := NewRedisTokenBucket(client)
	if err != nil {
		t.Fatalf("create limiter: %v", err)
	}

	ctx := context.Background()

	t.Run("capacity overflow", func(t *testing.T) {
		_, err := limiter.Allow(
			ctx,
			testRedisKey(t, "overflow-capacity"),
			int64(^uint64(0)>>1),
			1,
			1,
		)

		if !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf(
				"expected ErrInvalidCapacity, got %v",
				err,
			)
		}
	})

	t.Run("cost overflow", func(t *testing.T) {
		_, err := limiter.Allow(
			ctx,
			testRedisKey(t, "overflow-cost"),
			10,
			1,
			int64(^uint64(0)>>1),
		)

		if !errors.Is(err, ErrInvalidCost) {
			t.Fatalf(
				"expected ErrInvalidCost, got %v",
				err,
			)
		}
	})

	t.Run("NaN refill rate", func(t *testing.T) {
		_, err := limiter.Allow(
			ctx,
			testRedisKey(t, "nan-refill"),
			10,
			math.NaN(),
			1,
		)

		if !errors.Is(err, ErrInvalidRefillRate) {
			t.Fatalf(
				"expected ErrInvalidRefillRate, got %v",
				err,
			)
		}
	})

	t.Run("infinite refill rate", func(t *testing.T) {
		_, err := limiter.Allow(
			ctx,
			testRedisKey(t, "inf-refill"),
			10,
			math.Inf(1),
			1,
		)

		if !errors.Is(err, ErrInvalidRefillRate) {
			t.Fatalf(
				"expected ErrInvalidRefillRate, got %v",
				err,
			)
		}
	})
}
