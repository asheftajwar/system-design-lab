package policy

import (
	"context"
	"testing"
	"time"

	"github.com/asheftajwar/system-design-lab/02-rate-limiter/internal/limiter"
	"github.com/redis/go-redis/v9"
)

func newTestRedisClient() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:6379",
	})
}

func TestTokenBucketEnforcer_EnforcesPolicy(t *testing.T) {
	client := newTestRedisClient()

	ctx := context.Background()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}

	t.Cleanup(func() {
		_ = client.Close()
	})

	redisLimiter, err := limiter.NewRedisTokenBucket(client)
	if err != nil {
		t.Fatal(err)
	}

	enforcer, err := NewTokenBucketEnforcer(redisLimiter)
	if err != nil {
		t.Fatal(err)
	}

	key := "test:policy-adapter:" + time.Now().Format("20060102150405.000000000")

	policy := validPolicy("api-policy")
	policy.Limit = 2
	policy.RefillRate = 1

	first, err := enforcer.Allow(ctx, key, policy, 1)
	if err != nil {
		t.Fatal(err)
	}

	if !first.Allowed {
		t.Fatal("first request should be allowed")
	}

	if first.PolicyID != policy.ID {
		t.Fatalf(
			"expected policy ID %q, got %q",
			policy.ID,
			first.PolicyID,
		)
	}

	if first.PolicyVersion != policy.Version {
		t.Fatalf(
			"expected policy version %d, got %d",
			policy.Version,
			first.PolicyVersion,
		)
	}

	second, err := enforcer.Allow(ctx, key, policy, 1)
	if err != nil {
		t.Fatal(err)
	}

	if !second.Allowed {
		t.Fatal("second request should be allowed")
	}

	third, err := enforcer.Allow(ctx, key, policy, 1)
	if err != nil {
		t.Fatal(err)
	}

	if third.Allowed {
		t.Fatal("third request should be rejected")
	}

	if third.RetryAfter <= 0 {
		t.Fatalf(
			"expected positive RetryAfter, got %d",
			third.RetryAfter,
		)
	}
}

func TestTokenBucketEnforcer_RejectsWrongAlgorithm(t *testing.T) {
	client := newTestRedisClient()

	ctx := context.Background()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}

	t.Cleanup(func() {
		_ = client.Close()
	})

	redisLimiter, err := limiter.NewRedisTokenBucket(client)
	if err != nil {
		t.Fatal(err)
	}

	enforcer, err := NewTokenBucketEnforcer(redisLimiter)
	if err != nil {
		t.Fatal(err)
	}

	policy := validPolicy("wrong-algorithm")
	policy.Algorithm = AlgorithmFixedWindow
	policy.WindowSeconds = 60
	policy.RefillRate = 0

	_, err = enforcer.Allow(
		ctx,
		"test:policy-adapter:wrong-algorithm",
		policy,
		1,
	)

	if err != ErrInvalidAlgorithm {
		t.Fatalf(
			"expected ErrInvalidAlgorithm, got %v",
			err,
		)
	}
}

func TestEvaluator_WithRedisTokenBucket_MultiplePolicies(t *testing.T) {
	client := newTestRedisClient()

	ctx := context.Background()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}

	t.Cleanup(func() {
		_ = client.Close()
	})

	redisLimiter, err := limiter.NewRedisTokenBucket(client)
	if err != nil {
		t.Fatal(err)
	}

	enforcer, err := NewTokenBucketEnforcer(redisLimiter)
	if err != nil {
		t.Fatal(err)
	}

	evaluator, err := NewEvaluator(enforcer)
	if err != nil {
		t.Fatal(err)
	}

	request := validRequest()

	perSecond := validPolicy("per-second")
	perSecond.Limit = 2
	perSecond.RefillRate = 1

	perMinute := validPolicy("per-minute")
	perMinute.Limit = 100
	perMinute.RefillRate = 1

	policies := []Policy{
		perSecond,
		perMinute,
	}

	first, err := evaluator.Evaluate(
		ctx,
		request,
		policies,
	)
	if err != nil {
		t.Fatal(err)
	}

	if !first.Allowed {
		t.Fatal("first request should be allowed")
	}

	if len(first.Limits) != 2 {
		t.Fatalf(
			"expected 2 limit decisions, got %d",
			len(first.Limits),
		)
	}

	second, err := evaluator.Evaluate(
		ctx,
		request,
		policies,
	)
	if err != nil {
		t.Fatal(err)
	}

	if !second.Allowed {
		t.Fatal("second request should be allowed")
	}

	third, err := evaluator.Evaluate(
		ctx,
		request,
		policies,
	)
	if err != nil {
		t.Fatal(err)
	}

	if third.Allowed {
		t.Fatal("third request should be rejected")
	}

	if third.PolicyID != perSecond.ID {
		t.Fatalf(
			"expected rejecting policy %q, got %q",
			perSecond.ID,
			third.PolicyID,
		)
	}

	if third.PolicyVersion != perSecond.Version {
		t.Fatalf(
			"expected rejecting policy version %d, got %d",
			perSecond.Version,
			third.PolicyVersion,
		)
	}

	if third.RetryAfter <= 0 {
		t.Fatalf(
			"expected positive RetryAfter, got %d",
			third.RetryAfter,
		)
	}
}
