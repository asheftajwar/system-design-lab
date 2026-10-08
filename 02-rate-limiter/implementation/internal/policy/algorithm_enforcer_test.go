package policy

import (
	"context"
	"errors"
	"testing"
)

type fakeAlgorithmEnforcer struct {
	called    bool
	callCount int
	decision  LimitDecision
	err       error

	gotKey    string
	gotPolicy Policy
	gotCost   int64
}

func (f *fakeAlgorithmEnforcer) Allow(
	_ context.Context,
	key string,
	policy Policy,
	cost int64,
) (LimitDecision, error) {
	f.called = true
	f.callCount++
	f.gotKey = key
	f.gotPolicy = policy
	f.gotCost = cost

	return f.decision, f.err
}

func TestAlgorithmEnforcer_DispatchesByAlgorithm(t *testing.T) {
	tokenBucket := &fakeAlgorithmEnforcer{
		decision: LimitDecision{
			PolicyID: "token-policy",
			Allowed:  true,
		},
	}

	fixedWindow := &fakeAlgorithmEnforcer{
		decision: LimitDecision{
			PolicyID: "fixed-policy",
			Allowed:  true,
		},
	}

	slidingWindow := &fakeAlgorithmEnforcer{
		decision: LimitDecision{
			PolicyID: "sliding-policy",
			Allowed:  true,
		},
	}

	enforcer := NewAlgorithmEnforcer(map[Algorithm]Enforcer{
		AlgorithmTokenBucket:   tokenBucket,
		AlgorithmFixedWindow:   fixedWindow,
		AlgorithmSlidingWindow: slidingWindow,
	})

	tests := []struct {
		name             string
		algorithm        Algorithm
		expected         *fakeAlgorithmEnforcer
		expectedDecision LimitDecision
	}{
		{
			name:             "token bucket",
			algorithm:        AlgorithmTokenBucket,
			expected:         tokenBucket,
			expectedDecision: tokenBucket.decision,
		},
		{
			name:             "fixed window",
			algorithm:        AlgorithmFixedWindow,
			expected:         fixedWindow,
			expectedDecision: fixedWindow.decision,
		},
		{
			name:             "sliding window",
			algorithm:        AlgorithmSlidingWindow,
			expected:         slidingWindow,
			expectedDecision: slidingWindow.decision,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := Policy{
				ID:        "policy-" + tt.name,
				Version:   1,
				Algorithm: tt.algorithm,
			}

			key := "rl:test:" + tt.name
			cost := int64(3)

			decision, err := enforcer.Allow(
				context.Background(),
				key,
				policy,
				cost,
			)
			if err != nil {
				t.Fatalf("Allow() error = %v", err)
			}

			if decision != tt.expectedDecision {
				t.Fatalf(
					"decision = %+v, want %+v",
					decision,
					tt.expectedDecision,
				)
			}

			if !tt.expected.called {
				t.Fatal("expected selected enforcer to be called")
			}

			if tt.expected.callCount != 1 {
				t.Fatalf(
					"callCount = %d, want 1",
					tt.expected.callCount,
				)
			}

			if tt.expected.gotKey != key {
				t.Fatalf(
					"gotKey = %q, want %q",
					tt.expected.gotKey,
					key,
				)
			}

			if tt.expected.gotPolicy != policy {
				t.Fatalf(
					"gotPolicy = %+v, want %+v",
					tt.expected.gotPolicy,
					policy,
				)
			}

			if tt.expected.gotCost != cost {
				t.Fatalf(
					"gotCost = %d, want %d",
					tt.expected.gotCost,
					cost,
				)
			}
		})
	}
}

func TestAlgorithmEnforcer_ReturnsUnderlyingError(t *testing.T) {
	expectedErr := errors.New("enforcement failed")

	fake := &fakeAlgorithmEnforcer{
		err: expectedErr,
	}

	enforcer := NewAlgorithmEnforcer(map[Algorithm]Enforcer{
		AlgorithmTokenBucket: fake,
	})

	policy := Policy{
		ID:        "token-policy",
		Version:   1,
		Algorithm: AlgorithmTokenBucket,
	}

	_, err := enforcer.Allow(
		context.Background(),
		"rl:test",
		policy,
		1,
	)

	if !errors.Is(err, expectedErr) {
		t.Fatalf(
			"error = %v, want %v",
			err,
			expectedErr,
		)
	}
}

func TestAlgorithmEnforcer_RejectsUnsupportedAlgorithm(t *testing.T) {
	enforcer := NewAlgorithmEnforcer(map[Algorithm]Enforcer{})

	policy := Policy{
		ID:        "unsupported-policy",
		Version:   1,
		Algorithm: Algorithm("unsupported"),
	}

	_, err := enforcer.Allow(
		context.Background(),
		"rl:test",
		policy,
		1,
	)

	if !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf(
			"error = %v, want %v",
			err,
			ErrUnsupportedAlgorithm,
		)
	}
}

func TestAlgorithmEnforcer_CopiesEnforcerMap(t *testing.T) {
	fake := &fakeAlgorithmEnforcer{}

	original := map[Algorithm]Enforcer{
		AlgorithmTokenBucket: fake,
	}

	enforcer := NewAlgorithmEnforcer(original)

	delete(original, AlgorithmTokenBucket)

	policy := Policy{
		ID:        "token-policy",
		Version:   1,
		Algorithm: AlgorithmTokenBucket,
	}

	_, err := enforcer.Allow(
		context.Background(),
		"rl:test",
		policy,
		1,
	)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}

	if !fake.called {
		t.Fatal("expected copied map to retain token bucket enforcer")
	}
}