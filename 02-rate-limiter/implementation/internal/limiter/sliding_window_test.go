package limiter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

var slidingWindowTestKeyCounter uint64

func newTestSlidingWindow(t *testing.T) *RedisSlidingWindow {
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
		_ = client.Close()
	})

	return NewRedisSlidingWindow(client)
}

func slidingWindowTestKey(name string) string {
	n := atomic.AddUint64(&slidingWindowTestKeyCounter, 1)

	return fmt.Sprintf(
		"test:sliding-window:%s:%d",
		name,
		n,
	)
}

func TestRedisSlidingWindow_AllowsWithinLimit(t *testing.T) {
	ctx := context.Background()
	window := newTestSlidingWindow(t)

	decision, err := window.Allow(
		ctx,
		slidingWindowTestKey("allows"),
		5,
		60,
		1,
	)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}

	if !decision.Allowed {
		t.Fatal("request should be allowed")
	}

	if decision.Limit != 5 {
		t.Fatalf("Limit = %d, want 5", decision.Limit)
	}

	if decision.Remaining != 4 {
		t.Fatalf("Remaining = %d, want 4", decision.Remaining)
	}
}

func TestRedisSlidingWindow_RejectsAfterLimit(t *testing.T) {
	ctx := context.Background()
	window := newTestSlidingWindow(t)
	key := slidingWindowTestKey("rejects")

	for i := 0; i < 5; i++ {
		decision, err := window.Allow(ctx, key, 5, 60, 1)
		if err != nil {
			t.Fatalf("request %d: Allow() error = %v", i+1, err)
		}

		if !decision.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	decision, err := window.Allow(ctx, key, 5, 60, 1)
	if err != nil {
		t.Fatalf("sixth request: Allow() error = %v", err)
	}

	if decision.Allowed {
		t.Fatal("sixth request should be rejected")
	}

	if decision.Remaining != 0 {
		t.Fatalf("Remaining = %d, want 0", decision.Remaining)
	}

	if decision.RetryAfter <= 0 {
		t.Fatalf("RetryAfter = %d, want > 0", decision.RetryAfter)
	}
}

func TestRedisSlidingWindow_RequestCost(t *testing.T) {
	ctx := context.Background()
	window := newTestSlidingWindow(t)
	key := slidingWindowTestKey("cost")

	decision, err := window.Allow(ctx, key, 10, 60, 4)
	if err != nil {
		t.Fatalf("first request: Allow() error = %v", err)
	}

	if !decision.Allowed {
		t.Fatal("first request should be allowed")
	}

	if decision.Remaining != 6 {
		t.Fatalf("Remaining = %d, want 6", decision.Remaining)
	}

	decision, err = window.Allow(ctx, key, 10, 60, 6)
	if err != nil {
		t.Fatalf("second request: Allow() error = %v", err)
	}

	if !decision.Allowed {
		t.Fatal("second request should be allowed")
	}

	if decision.Remaining != 0 {
		t.Fatalf("Remaining = %d, want 0", decision.Remaining)
	}
}

func TestRedisSlidingWindow_RejectedRequestDoesNotConsumeQuota(t *testing.T) {
	ctx := context.Background()
	window := newTestSlidingWindow(t)
	key := slidingWindowTestKey("rejected-no-consume")

	decision, err := window.Allow(ctx, key, 5, 60, 5)
	if err != nil {
		t.Fatalf("first request: Allow() error = %v", err)
	}

	if !decision.Allowed {
		t.Fatal("first request should be allowed")
	}

	decision, err = window.Allow(ctx, key, 5, 60, 1)
	if err != nil {
		t.Fatalf("rejected request: Allow() error = %v", err)
	}

	if decision.Allowed {
		t.Fatal("request should be rejected")
	}

	// The rejected request must not change usage.
	decision, err = window.Allow(ctx, key, 5, 60, 5)
	if err != nil {
		t.Fatalf("second rejected request: Allow() error = %v", err)
	}

	if decision.Allowed {
		t.Fatal("request should still be rejected")
	}

	if decision.Remaining != 0 {
		t.Fatalf("Remaining = %d, want 0", decision.Remaining)
	}
}

func TestRedisSlidingWindow_CostGreaterThanLimit(t *testing.T) {
	ctx := context.Background()
	window := newTestSlidingWindow(t)
	key := slidingWindowTestKey("cost-too-large")

	decision, err := window.Allow(ctx, key, 5, 60, 6)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}

	if decision.Allowed {
		t.Fatal("request should be rejected")
	}

	if decision.Remaining != 5 {
		t.Fatalf("Remaining = %d, want 5", decision.Remaining)
	}

	if decision.RetryAfter != 0 {
		t.Fatalf("RetryAfter = %d, want 0", decision.RetryAfter)
	}

	// The oversized request must not create quota usage.
	decision, err = window.Allow(ctx, key, 5, 60, 5)
	if err != nil {
		t.Fatalf("valid request after oversized request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("valid request should be allowed")
	}
}

func TestRedisSlidingWindow_RollingExpiration(t *testing.T) {
	ctx := context.Background()
	window := newTestSlidingWindow(t)
	key := slidingWindowTestKey("rolling-expiration")

	decision, err := window.Allow(ctx, key, 1, 1, 1)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("first request should be allowed")
	}

	decision, err = window.Allow(ctx, key, 1, 1, 1)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}

	if decision.Allowed {
		t.Fatal("second request should be rejected")
	}

	time.Sleep(1100 * time.Millisecond)

	decision, err = window.Allow(ctx, key, 1, 1, 1)
	if err != nil {
		t.Fatalf("request after expiration: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("request should be allowed after the previous request expires")
	}
}

func TestRedisSlidingWindow_RetryAfterAccountsForRequestCost(t *testing.T) {
	ctx := context.Background()
	window := newTestSlidingWindow(t)
	key := slidingWindowTestKey("retry-cost")

	// Usage becomes 8.
	decision, err := window.Allow(ctx, key, 10, 2, 3)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("first request should be allowed")
	}

	decision, err = window.Allow(ctx, key, 10, 2, 5)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("second request should be allowed")
	}

	// Usage is now 8. A cost-4 request needs 2 units to expire.
	decision, err = window.Allow(ctx, key, 10, 2, 4)
	if err != nil {
		t.Fatalf("third request: %v", err)
	}

	if decision.Allowed {
		t.Fatal("third request should be rejected")
	}

	if decision.Remaining != 2 {
		t.Fatalf("Remaining = %d, want 2", decision.Remaining)
	}

	if decision.RetryAfter <= 0 {
		t.Fatalf("RetryAfter = %d, want > 0", decision.RetryAfter)
	}
}

func TestRedisSlidingWindow_SharedInstancesShareState(t *testing.T) {
	ctx := context.Background()

	window1 := newTestSlidingWindow(t)
	window2 := newTestSlidingWindow(t)

	key := slidingWindowTestKey("shared")

	decision, err := window1.Allow(ctx, key, 2, 60, 1)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("first request should be allowed")
	}

	decision, err = window2.Allow(ctx, key, 2, 60, 1)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("second request should be allowed")
	}

	decision, err = window1.Allow(ctx, key, 2, 60, 1)
	if err != nil {
		t.Fatalf("third request: %v", err)
	}

	if decision.Allowed {
		t.Fatal("third request should be rejected")
	}
}

func TestRedisSlidingWindow_ConcurrentRequestsCannotOversubscribe(t *testing.T) {
	ctx := context.Background()
	window := newTestSlidingWindow(t)

	key := slidingWindowTestKey("concurrent")

	const (
		limit       = 20
		requests    = 100
		windowSecs  = 60
		requestCost = 1
	)

	var (
		wg      sync.WaitGroup
		allowed int64
	)

	for i := 0; i < requests; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			decision, err := window.Allow(
				ctx,
				key,
				limit,
				windowSecs,
				requestCost,
			)
			if err != nil {
				t.Errorf("Allow() error = %v", err)
				return
			}

			if decision.Allowed {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}

	wg.Wait()

	if allowed != limit {
		t.Fatalf("allowed = %d, want %d", allowed, limit)
	}
}

func TestRedisSlidingWindow_InvalidConfiguration(t *testing.T) {
	ctx := context.Background()
	window := newTestSlidingWindow(t)
	key := slidingWindowTestKey("invalid")

	tests := []struct {
		name        string
		limit       int64
		window      int64
		cost        int64
		expectedErr error
	}{
		{
			name:        "invalid limit",
			limit:       0,
			window:      60,
			cost:        1,
			expectedErr: ErrInvalidSlidingWindowLimit,
		},
		{
			name:        "negative limit",
			limit:       -1,
			window:      60,
			cost:        1,
			expectedErr: ErrInvalidSlidingWindowLimit,
		},
		{
			name:        "invalid window",
			limit:       10,
			window:      0,
			cost:        1,
			expectedErr: ErrInvalidSlidingWindowWindow,
		},
		{
			name:        "negative window",
			limit:       10,
			window:      -1,
			cost:        1,
			expectedErr: ErrInvalidSlidingWindowWindow,
		},
		{
			name:        "invalid cost",
			limit:       10,
			window:      60,
			cost:        0,
			expectedErr: ErrInvalidSlidingWindowCost,
		},
		{
			name:        "negative cost",
			limit:       10,
			window:      60,
			cost:        -1,
			expectedErr: ErrInvalidSlidingWindowCost,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := window.Allow(
				ctx,
				key,
				tt.limit,
				tt.window,
				tt.cost,
			)

			if !errors.Is(err, tt.expectedErr) {
				t.Fatalf(
					"error = %v, want %v",
					err,
					tt.expectedErr,
				)
			}
		})
	}
}
