package policy

import (
	"context"
	"errors"
	"testing"
	"fmt"
	"sync/atomic"

	"github.com/redis/go-redis/v9"

	"github.com/asheftajwar/system-design-lab/02-rate-limiter/internal/limiter"
)

var slidingWindowEnforcerTestKeyCounter uint64

func slidingWindowEnforcerTestKey(name string) string {
	n := atomic.AddUint64(&slidingWindowEnforcerTestKeyCounter, 1)
	return fmt.Sprintf(
		"test:policy:sliding-window:%s:%d",
		name,
		n,
	)
}

func newTestSlidingWindowEnforcer(t *testing.T) *SlidingWindowEnforcer {
	t.Helper()

	client := redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:6379",
	})

	ctx := context.Background()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Skipf("Redis unavailable: %v", err)
	}

	t.Cleanup(func() {
		_ = client.Close()
	})

	return NewSlidingWindowEnforcer(
		limiter.NewRedisSlidingWindow(client),
	)
}

func TestSlidingWindowEnforcer_AllowsAndMapsDecision(t *testing.T) {
	ctx := context.Background()
	enforcer := newTestSlidingWindowEnforcer(t)

	policy := Policy{
		ID:              "swl-policy",
		Version:         3,
		Name:            "Sliding window",
		Algorithm:       AlgorithmSlidingWindow,
		Limit:           10,
		WindowSeconds:   60,
		RequestCost:     1,
		IdentityType:    IdentityUser,
		Scope:           ScopeIdentity,
		EnforcementMode: EnforcementStrict,
		Enabled:         true,
	}

	decision, err := enforcer.Allow(
		ctx,
		"test:policy:sliding-window:allows",
		policy,
		3,
	)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}

	if !decision.Allowed {
		t.Fatal("request should be allowed")
	}

	if decision.PolicyID != policy.ID {
		t.Fatalf("PolicyID = %q, want %q", decision.PolicyID, policy.ID)
	}

	if decision.PolicyVersion != policy.Version {
		t.Fatalf(
			"PolicyVersion = %d, want %d",
			decision.PolicyVersion,
			policy.Version,
		)
	}

	if decision.Limit != 10 {
		t.Fatalf("Limit = %d, want 10", decision.Limit)
	}

	if decision.Remaining != 7 {
		t.Fatalf("Remaining = %d, want 7", decision.Remaining)
	}
}

func TestSlidingWindowEnforcer_RejectsAfterLimit(t *testing.T) {
	ctx := context.Background()
	enforcer := newTestSlidingWindowEnforcer(t)

	policy := Policy{
		ID:              "swl-reject-policy",
		Version:         1,
		Algorithm:       AlgorithmSlidingWindow,
		Limit:           5,
		WindowSeconds:   60,
		IdentityType:    IdentityUser,
		Scope:           ScopeIdentity,
		EnforcementMode: EnforcementStrict,
		Enabled:         true,
	}

	key := slidingWindowEnforcerTestKey("reject")

	for i := 0; i < 5; i++ {
		decision, err := enforcer.Allow(ctx, key, policy, 1)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}

		if !decision.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	decision, err := enforcer.Allow(ctx, key, policy, 1)
	if err != nil {
		t.Fatalf("sixth request: %v", err)
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

func TestSlidingWindowEnforcer_PreservesRequestCost(t *testing.T) {
	ctx := context.Background()
	enforcer := newTestSlidingWindowEnforcer(t)

	policy := Policy{
		ID:              "swl-cost-policy",
		Version:         1,
		Algorithm:       AlgorithmSlidingWindow,
		Limit:           10,
		WindowSeconds:   60,
		IdentityType:    IdentityUser,
		Scope:           ScopeIdentity,
		EnforcementMode: EnforcementStrict,
		Enabled:         true,
	}

	key := slidingWindowEnforcerTestKey("cost")
	

	decision, err := enforcer.Allow(ctx, key, policy, 4)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}

	if !decision.Allowed {
		t.Fatal("first request should be allowed")
	}

	if decision.Remaining != 6 {
		t.Fatalf("Remaining = %d, want 6", decision.Remaining)
	}
}

func TestSlidingWindowEnforcer_RejectsWrongAlgorithm(t *testing.T) {
	ctx := context.Background()
	enforcer := newTestSlidingWindowEnforcer(t)

	policy := Policy{
		ID:              "wrong-algorithm",
		Version:         1,
		Algorithm:       AlgorithmTokenBucket,
		Limit:           10,
		RefillRate:      1,
		IdentityType:    IdentityUser,
		Scope:           ScopeIdentity,
		EnforcementMode: EnforcementStrict,
		Enabled:         true,
	}

	_, err := enforcer.Allow(
		ctx,
		"test:policy:sliding-window:wrong-algorithm",
		policy,
		1,
	)
	if !errors.Is(err, ErrWrongSlidingWindowAlgorithm) {
		t.Fatalf(
			"error = %v, want %v",
			err,
			ErrWrongSlidingWindowAlgorithm,
		)
	}
}
