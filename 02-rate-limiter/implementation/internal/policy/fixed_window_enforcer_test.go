package policy

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/asheftajwar/system-design-lab/02-rate-limiter/internal/limiter"
)

var fixedWindowEnforcerTestKeyCounter uint64

func fixedWindowEnforcerTestKey(name string) string {
	n := atomic.AddUint64(&fixedWindowEnforcerTestKeyCounter, 1)
	return fmt.Sprintf(
		"test:policy:fixed-window:%s:%d",
		name,
		n,
	)
}

func newTestFixedWindowEnforcer(t *testing.T) *FixedWindowEnforcer {
	t.Helper()

	client := redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:6379",
	})

	ctx := context.Background()

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		t.Skipf("redis unavailable: %v", err)
	}

	t.Cleanup(func() {
		client.Close()
	})

	return NewFixedWindowEnforcer(
		limiter.NewRedisFixedWindow(client),
	)
}

func TestFixedWindowEnforcer_EnforcesPolicy(t *testing.T) {
	ctx := context.Background()
	enforcer := newTestFixedWindowEnforcer(t)

	policy := Policy{
		ID:              "fixed-window-policy",
		Version:         3,
		Name:            "requests-per-minute",
		Algorithm:       AlgorithmFixedWindow,
		Limit:           10,
		WindowSeconds:   60,
		RequestCost:     1,
		IdentityType:    IdentityAPIKey,
		Scope:           ScopeTenant,
		EnforcementMode: EnforcementStrict,
		Enabled:         true,
	}

	decision, err := enforcer.Allow(
		ctx,
		fixedWindowEnforcerTestKey("enforce"),
		policy,
		1,
	)

	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}

	if !decision.Allowed {
		t.Fatal("request should be allowed")
	}

	if decision.PolicyID != policy.ID {
		t.Fatalf(
			"PolicyID = %q, want %q",
			decision.PolicyID,
			policy.ID,
		)
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

	if decision.Remaining != 9 {
		t.Fatalf("Remaining = %d, want 9", decision.Remaining)
	}
}

func TestFixedWindowEnforcer_RejectsWrongAlgorithm(t *testing.T) {
	ctx := context.Background()
	enforcer := newTestFixedWindowEnforcer(t)

	policy := Policy{
		ID:              "token-bucket-policy",
		Version:         1,
		Name:            "token-bucket",
		Algorithm:       AlgorithmTokenBucket,
		Limit:           10,
		RefillRate:      1,
		RequestCost:     1,
		IdentityType:    IdentityAPIKey,
		Scope:           ScopeTenant,
		EnforcementMode: EnforcementStrict,
		Enabled:         true,
	}

	_, err := enforcer.Allow(
		ctx,
		"test:policy:fixed-window:wrong-algorithm",
		policy,
		1,
	)
	if !errors.Is(err, ErrWrongFixedWindowAlgorithm) {
		t.Fatalf(
			"error = %v, want %v",
			err,
			ErrWrongFixedWindowAlgorithm,
		)
	}
}

func TestFixedWindowEnforcer_RequestCost(t *testing.T) {
	ctx := context.Background()
	enforcer := newTestFixedWindowEnforcer(t)

	policy := Policy{
		ID:              "cost-policy",
		Version:         1,
		Name:            "weighted-requests",
		Algorithm:       AlgorithmFixedWindow,
		Limit:           10,
		WindowSeconds:   60,
		RequestCost:     1,
		IdentityType:    IdentityAPIKey,
		Scope:           ScopeTenant,
		EnforcementMode: EnforcementStrict,
		Enabled:         true,
	}

	key := fixedWindowEnforcerTestKey("cost")

	decision, err := enforcer.Allow(ctx, key, policy, 4)
	if err != nil {
		t.Fatalf("first Allow() error = %v", err)
	}

	if !decision.Allowed {
		t.Fatal("cost 4 request should be allowed")
	}

	if decision.Remaining != 6 {
		t.Fatalf("Remaining = %d, want 6", decision.Remaining)
	}

	decision, err = enforcer.Allow(ctx, key, policy, 6)
	if err != nil {
		t.Fatalf("second Allow() error = %v", err)
	}

	if !decision.Allowed {
		t.Fatal("cost 6 request should be allowed")
	}

	if decision.Remaining != 0 {
		t.Fatalf("Remaining = %d, want 0", decision.Remaining)
	}

	decision, err = enforcer.Allow(ctx, key, policy, 1)
	if err != nil {
		t.Fatalf("third Allow() error = %v", err)
	}

	if decision.Allowed {
		t.Fatal("request should be rejected after quota exhaustion")
	}

	if decision.Remaining != 0 {
		t.Fatalf("Remaining = %d, want 0", decision.Remaining)
	}
}

func TestFixedWindowEnforcer_PreservesPolicyMetadata(t *testing.T) {
	ctx := context.Background()
	enforcer := newTestFixedWindowEnforcer(t)

	policy := Policy{
		ID:              "metadata-policy",
		Version:         42,
		Name:            "metadata-test",
		Algorithm:       AlgorithmFixedWindow,
		Limit:           100,
		WindowSeconds:   60,
		RequestCost:     1,
		IdentityType:    IdentityAPIKey,
		Scope:           ScopeTenant,
		EnforcementMode: EnforcementStrict,
		Enabled:         true,
	}

	decision, err := enforcer.Allow(
		ctx,
		"test:policy:fixed-window:metadata",
		policy,
		1,
	)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}

	if decision.PolicyID != "metadata-policy" {
		t.Fatalf(
			"PolicyID = %q, want %q",
			decision.PolicyID,
			"metadata-policy",
		)
	}

	if decision.PolicyVersion != 42 {
		t.Fatalf(
			"PolicyVersion = %d, want 42",
			decision.PolicyVersion,
		)
	}
}
