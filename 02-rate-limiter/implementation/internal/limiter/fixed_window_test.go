package limiter

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

var testKeyCounter uint64

func testKey(name string) string {
	n := atomic.AddUint64(&testKeyCounter, 1)
	return fmt.Sprintf("test:fixed-window:%s:%d", name, n)
}

func newTestRedisFixedWindow(t *testing.T) *RedisFixedWindow {
	t.Helper()

	client := redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:6379",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		t.Skipf("redis unavailable: %v", err)
	}

	t.Cleanup(func() {
		client.Close()
	})

	return NewRedisFixedWindow(client)
}

func TestRedisFixedWindow_EnforcesLimit(t *testing.T) {
	ctx := context.Background()
	fixedWindow := newTestRedisFixedWindow(t)

	key := testKey("enforces-limit")

	decision, err := fixedWindow.Allow(ctx, key, 3, 10, 1)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("first request should be allowed")
	}

	if decision.Remaining != 2 {
		t.Fatalf("remaining = %d, want 2", decision.Remaining)
	}

	decision, err = fixedWindow.Allow(ctx, key, 3, 10, 1)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("second request should be allowed")
	}

	if decision.Remaining != 1 {
		t.Fatalf("remaining = %d, want 1", decision.Remaining)
	}

	decision, err = fixedWindow.Allow(ctx, key, 3, 10, 1)
	if err != nil {
		t.Fatalf("third request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("third request should be allowed")
	}

	if decision.Remaining != 0 {
		t.Fatalf("remaining = %d, want 0", decision.Remaining)
	}

	decision, err = fixedWindow.Allow(ctx, key, 3, 10, 1)
	if err != nil {
		t.Fatalf("fourth request: %v", err)
	}

	if decision.Allowed {
		t.Fatal("fourth request should be rejected")
	}

	if decision.Remaining != 0 {
		t.Fatalf("remaining = %d, want 0", decision.Remaining)
	}
}

func TestRedisFixedWindow_RequestCost(t *testing.T) {
	ctx := context.Background()
	fixedWindow := newTestRedisFixedWindow(t)

	key := testKey("request-cost")

	decision, err := fixedWindow.Allow(ctx, key, 10, 10, 4)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("first request should be allowed")
	}

	if decision.Remaining != 6 {
		t.Fatalf("remaining = %d, want 6", decision.Remaining)
	}

	decision, err = fixedWindow.Allow(ctx, key, 10, 10, 3)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("second request should be allowed")
	}

	if decision.Remaining != 3 {
		t.Fatalf("remaining = %d, want 3", decision.Remaining)
	}

	decision, err = fixedWindow.Allow(ctx, key, 10, 10, 4)
	if err != nil {
		t.Fatalf("third request: %v", err)
	}

	if decision.Allowed {
		t.Fatal("third request should be rejected")
	}

	if decision.Remaining != 3 {
		t.Fatalf("remaining = %d, want 3", decision.Remaining)
	}
}

func TestRedisFixedWindow_RejectedRequestDoesNotConsumeQuota(t *testing.T) {
	ctx := context.Background()
	fixedWindow := newTestRedisFixedWindow(t)

	key := testKey("rejected-does-not-consume")

	decision, err := fixedWindow.Allow(ctx, key, 5, 10, 5)
	if err != nil {
		t.Fatalf("initial request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("initial request should be allowed")
	}

	decision, err = fixedWindow.Allow(ctx, key, 5, 10, 1)
	if err != nil {
		t.Fatalf("rejected request: %v", err)
	}

	if decision.Allowed {
		t.Fatal("request should be rejected")
	}

	if decision.Remaining != 0 {
		t.Fatalf("remaining = %d, want 0", decision.Remaining)
	}

	// The quota remains exhausted. This verifies that the rejected
	// request did not mutate the counter into an invalid state.
	decision, err = fixedWindow.Allow(ctx, key, 5, 10, 1)
	if err != nil {
		t.Fatalf("second rejected request: %v", err)
	}

	if decision.Allowed {
		t.Fatal("request should still be rejected")
	}

	if decision.Remaining != 0 {
		t.Fatalf("remaining = %d, want 0", decision.Remaining)
	}
}

func TestRedisFixedWindow_ResetsAtWindowBoundary(t *testing.T) {
	ctx := context.Background()
	fixedWindow := newTestRedisFixedWindow(t)

	key := testKey("boundary")

	decision, err := fixedWindow.Allow(ctx, key, 2, 1, 2)
	if err != nil {
		t.Fatalf("initial request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("initial request should be allowed")
	}

	if decision.Remaining != 0 {
		t.Fatalf("remaining = %d, want 0", decision.Remaining)
	}

	decision, err = fixedWindow.Allow(ctx, key, 2, 1, 1)
	if err != nil {
		t.Fatalf("rejected request: %v", err)
	}

	if decision.Allowed {
		t.Fatal("request should be rejected before window reset")
	}

	// Redis TIME is authoritative, so sleep until the next window.
	time.Sleep(1200 * time.Millisecond)

	decision, err = fixedWindow.Allow(ctx, key, 2, 1, 1)
	if err != nil {
		t.Fatalf("request after reset: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("request should be allowed after window reset")
	}

	if decision.Remaining != 1 {
		t.Fatalf("remaining = %d, want 1", decision.Remaining)
	}
}

func TestRedisFixedWindow_TTLExpiresWithWindow(t *testing.T) {
	ctx := context.Background()
	fixedWindow := newTestRedisFixedWindow(t)

	key := testKey("ttl")

	decision, err := fixedWindow.Allow(ctx, key, 10, 2, 1)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("request should be allowed")
	}

	ttl, err := fixedWindow.client.TTL(ctx, key+":fw").Result()

	if err != nil {
		t.Fatalf("read ttl: %v", err)
	}

	if ttl <= 0 {
		t.Fatalf("ttl = %v, want positive ttl", ttl)
	}

	if ttl > 2*time.Second {
		t.Fatalf("ttl = %v, should not exceed window duration", ttl)
	}
}

func TestRedisFixedWindow_CostGreaterThanLimit(t *testing.T) {
	ctx := context.Background()
	fixedWindow := newTestRedisFixedWindow(t)

	key := testKey("cost-too-large")

	decision, err := fixedWindow.Allow(ctx, key, 5, 10, 6)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if decision.Allowed {
		t.Fatal("request should be rejected")
	}

	if decision.Limit != 5 {
		t.Fatalf("limit = %d, want 5", decision.Limit)
	}

	if decision.Remaining != 5 {
		t.Fatalf("remaining = %d, want 5", decision.Remaining)
	}

	// Cost greater than limit must not create/consume the bucket.
	decision, err = fixedWindow.Allow(ctx, key, 5, 10, 5)
	if err != nil {
		t.Fatalf("follow-up request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("follow-up request should be allowed")
	}
}

func TestRedisFixedWindow_InvalidConfiguration(t *testing.T) {
	ctx := context.Background()
	fixedWindow := newTestRedisFixedWindow(t)

	tests := []struct {
		name          string
		limit         int64
		windowSeconds int64
		cost          int64
		wantErr       error
	}{
		{
			name:          "invalid limit",
			limit:         0,
			windowSeconds: 10,
			cost:          1,
			wantErr:       ErrInvalidFixedWindowLimit,
		},
		{
			name:          "negative limit",
			limit:         -1,
			windowSeconds: 10,
			cost:          1,
			wantErr:       ErrInvalidFixedWindowLimit,
		},
		{
			name:          "invalid window",
			limit:         10,
			windowSeconds: 0,
			cost:          1,
			wantErr:       ErrInvalidFixedWindowWindow,
		},
		{
			name:          "negative window",
			limit:         10,
			windowSeconds: -1,
			cost:          1,
			wantErr:       ErrInvalidFixedWindowWindow,
		},
		{
			name:          "invalid cost",
			limit:         10,
			windowSeconds: 10,
			cost:          0,
			wantErr:       ErrInvalidFixedWindowCost,
		},
		{
			name:          "negative cost",
			limit:         10,
			windowSeconds: 10,
			cost:          -1,
			wantErr:       ErrInvalidFixedWindowCost,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fixedWindow.Allow(
				ctx,
				"test:fixed-window:invalid:"+tt.name,
				tt.limit,
				tt.windowSeconds,
				tt.cost,
			)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRedisFixedWindow_SharedInstances(t *testing.T) {
	ctx := context.Background()

	first := newTestRedisFixedWindow(t)
	second := newTestRedisFixedWindow(t)

	key := testKey("shared-instances")

	decision, err := first.Allow(ctx, key, 2, 10, 1)
	if err != nil {
		t.Fatalf("first instance request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("first request should be allowed")
	}

	decision, err = second.Allow(ctx, key, 2, 10, 1)
	if err != nil {
		t.Fatalf("second instance request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("second request should be allowed")
	}

	decision, err = first.Allow(ctx, key, 2, 10, 1)
	if err != nil {
		t.Fatalf("third request: %v", err)
	}

	if decision.Allowed {
		t.Fatal("third request should be rejected")
	}
}
