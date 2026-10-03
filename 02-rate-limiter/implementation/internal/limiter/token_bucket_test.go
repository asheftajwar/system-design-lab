package limiter

import (
	"testing"
	"time"
)

func TestTokenBucketAllowsRequestsWithinCapacity(t *testing.T) {
	now := time.Unix(0, 0)

	bucket, err := NewTokenBucket(10, 1, now)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		decision, err := bucket.Allow(now, 1)
		if err != nil {
			t.Fatal(err)
		}

		if !decision.Allowed {
			t.Fatalf("request %d should have been allowed", i+1)
		}
	}

	decision, err := bucket.Allow(now, 1)
	if err != nil {
		t.Fatal(err)
	}

	if decision.Allowed {
		t.Fatal("11th request should have been rejected")
	}

	if decision.Remaining != 0 {
		t.Fatalf("expected 0 remaining tokens, got %d", decision.Remaining)
	}
}

func TestTokenBucketRefillsOverTime(t *testing.T) {
	now := time.Unix(0, 0)

	bucket, err := NewTokenBucket(10, 2, now)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		if _, err := bucket.Allow(now, 1); err != nil {
			t.Fatal(err)
		}
	}

	later := now.Add(2 * time.Second)

	decision, err := bucket.Allow(later, 1)
	if err != nil {
		t.Fatal(err)
	}

	if !decision.Allowed {
		t.Fatal("request should have been allowed after refill")
	}

	if decision.Remaining != 3 {
		t.Fatalf("expected 3 remaining tokens, got %d", decision.Remaining)
	}
}

func TestTokenBucketDoesNotExceedCapacity(t *testing.T) {
	now := time.Unix(0, 0)

	bucket, err := NewTokenBucket(10, 100, now)
	if err != nil {
		t.Fatal(err)
	}

	decision, err := bucket.Allow(now.Add(time.Hour), 1)
	if err != nil {
		t.Fatal(err)
	}

	if !decision.Allowed {
		t.Fatal("request should have been allowed")
	}

	if decision.Remaining != 9 {
		t.Fatalf("expected 9 remaining tokens, got %d", decision.Remaining)
	}
}

func TestTokenBucketSupportsRequestCost(t *testing.T) {
	now := time.Unix(0, 0)

	bucket, err := NewTokenBucket(10, 1, now)
	if err != nil {
		t.Fatal(err)
	}

	decision, err := bucket.Allow(now, 5)
	if err != nil {
		t.Fatal(err)
	}

	if !decision.Allowed {
		t.Fatal("request with cost 5 should have been allowed")
	}

	if decision.Remaining != 5 {
		t.Fatalf("expected 5 remaining tokens, got %d", decision.Remaining)
	}

	decision, err = bucket.Allow(now, 6)
	if err != nil {
		t.Fatal(err)
	}

	if decision.Allowed {
		t.Fatal("request with insufficient tokens should have been rejected")
	}
}

func TestTokenBucketReportsRetryAfter(t *testing.T) {
	now := time.Unix(0, 0)

	bucket, err := NewTokenBucket(10, 2, now)
	if err != nil {
		t.Fatal(err)
	}

	_, err = bucket.Allow(now, 10)
	if err != nil {
		t.Fatal(err)
	}

	decision, err := bucket.Allow(now, 4)
	if err != nil {
		t.Fatal(err)
	}

	if decision.Allowed {
		t.Fatal("request should have been rejected")
	}

	if decision.RetryAfter != 2 {
		t.Fatalf("expected retry after 2 seconds, got %d", decision.RetryAfter)
	}
}

func TestTokenBucketRejectsInvalidConfiguration(t *testing.T) {
	now := time.Unix(0, 0)

	if _, err := NewTokenBucket(0, 1, now); err != ErrInvalidCapacity {
		t.Fatalf("expected ErrInvalidCapacity, got %v", err)
	}

	if _, err := NewTokenBucket(10, 0, now); err != ErrInvalidRefillRate {
		t.Fatalf("expected ErrInvalidRefillRate, got %v", err)
	}
}

func TestTokenBucketRejectsInvalidCost(t *testing.T) {
	now := time.Unix(0, 0)

	bucket, err := NewTokenBucket(10, 1, now)
	if err != nil {
		t.Fatal(err)
	}

	_, err = bucket.Allow(now, 0)
	if err != ErrInvalidCost {
		t.Fatalf("expected ErrInvalidCost, got %v", err)
	}
}

func TestTokenBucketRejectsCostGreaterThanCapacity(t *testing.T) {
	now := time.Unix(0, 0)

	bucket, err := NewTokenBucket(10, 1, now)
	if err != nil {
		t.Fatal(err)
	}

	decision, err := bucket.Allow(now, 11)
	if err != nil {
		t.Fatal(err)
	}

	if decision.Allowed {
		t.Fatal("request cost greater than capacity should be rejected")
	}
}
