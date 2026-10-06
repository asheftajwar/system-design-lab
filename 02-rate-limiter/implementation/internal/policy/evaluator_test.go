package policy

import (
	"context"
	"errors"
	"testing"
)

type fakeEnforcer struct {
	decisions map[string]LimitDecision
	calls     []string
}

func (f *fakeEnforcer) Allow(
	_ context.Context,
	_ string,
	policy Policy,
	_ int64,
) (LimitDecision, error) {
	f.calls = append(f.calls, policy.ID)

	decision, ok := f.decisions[policy.ID]
	if !ok {
		return LimitDecision{}, errors.New("missing fake decision")
	}

	return decision, nil
}

func validRequest() Request {
	return Request{
		Identity: Identity{
			Type:  IdentityAPIKey,
			Value: "key-123",
		},
		TenantID: "tenant-123",
		Resource: Resource{
			Method: "GET",
			Path:   "/users",
		},
		Cost: 1,
	}
}

func validPolicy(id string) Policy {
	return Policy{
		ID:              id,
		Version:         1,
		Name:            id,
		Algorithm:       AlgorithmTokenBucket,
		Limit:           100,
		RefillRate:      10,
		RequestCost:     1,
		IdentityType:    IdentityAPIKey,
		Scope:           ScopeTenant,
		EnforcementMode: EnforcementStrict,
		Enabled:         true,
	}
}

func TestEvaluator_AllPoliciesMustAllow(t *testing.T) {
	enforcer := &fakeEnforcer{
		decisions: map[string]LimitDecision{
			"per-second": {
				PolicyID:  "per-second",
				Allowed:   true,
				Limit:     20,
				Remaining: 10,
			},
			"per-minute": {
				PolicyID:  "per-minute",
				Allowed:   true,
				Limit:     500,
				Remaining: 400,
			},
		},
	}

	evaluator, err := NewEvaluator(enforcer)
	if err != nil {
		t.Fatal(err)
	}

	decision, err := evaluator.Evaluate(
		context.Background(),
		validRequest(),
		[]Policy{
			validPolicy("per-second"),
			validPolicy("per-minute"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if !decision.Allowed {
		t.Fatal("expected request to be allowed")
	}

	if len(decision.Limits) != 2 {
		t.Fatalf("expected 2 limit decisions, got %d", len(decision.Limits))
	}
}

func TestEvaluator_RejectsWhenAnyPolicyRejects(t *testing.T) {
	enforcer := &fakeEnforcer{
		decisions: map[string]LimitDecision{
			"per-second": {
				PolicyID:  "per-second",
				Allowed:   true,
				Limit:     20,
				Remaining: 0,
			},
			"per-minute": {
				PolicyID:   "per-minute",
				Allowed:    false,
				Limit:      500,
				Remaining:  0,
				RetryAfter: 12,
				ResetAfter: 30,
			},
		},
	}

	evaluator, err := NewEvaluator(enforcer)
	if err != nil {
		t.Fatal(err)
	}

	decision, err := evaluator.Evaluate(
		context.Background(),
		validRequest(),
		[]Policy{
			validPolicy("per-second"),
			validPolicy("per-minute"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if decision.Allowed {
		t.Fatal("expected request to be rejected")
	}

	if decision.PolicyID != "per-minute" {
		t.Fatalf(
			"expected rejecting policy per-minute, got %q",
			decision.PolicyID,
		)
	}

	if decision.RetryAfter != 12 {
		t.Fatalf(
			"expected retry after 12, got %d",
			decision.RetryAfter,
		)
	}
}

func TestEvaluator_SkipsDisabledPolicies(t *testing.T) {
	enforcer := &fakeEnforcer{
		decisions: map[string]LimitDecision{
			"active": {
				PolicyID:  "active",
				Allowed:   true,
				Limit:     100,
				Remaining: 50,
			},
		},
	}

	disabled := validPolicy("disabled")
	disabled.Enabled = false

	evaluator, err := NewEvaluator(enforcer)
	if err != nil {
		t.Fatal(err)
	}

	decision, err := evaluator.Evaluate(
		context.Background(),
		validRequest(),
		[]Policy{
			disabled,
			validPolicy("active"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(decision.Limits) != 1 {
		t.Fatalf(
			"expected 1 evaluated policy, got %d",
			len(decision.Limits),
		)
	}

	if !decision.Allowed {
		t.Fatal("expected request to be allowed")
	}
}

func TestEvaluator_RejectsInvalidRequest(t *testing.T) {
	enforcer := &fakeEnforcer{
		decisions: map[string]LimitDecision{},
	}

	evaluator, err := NewEvaluator(enforcer)
	if err != nil {
		t.Fatal(err)
	}

	request := validRequest()
	request.Cost = 0

	_, err = evaluator.Evaluate(
		context.Background(),
		request,
		[]Policy{validPolicy("policy")},
	)

	if !errors.Is(err, ErrInvalidRequest) &&
		!errors.Is(err, ErrInvalidRequestCost) {
		t.Fatalf("expected invalid request error, got %v", err)
	}
}

func TestEvaluator_RejectsNoPolicies(t *testing.T) {
	enforcer := &fakeEnforcer{}

	evaluator, err := NewEvaluator(enforcer)
	if err != nil {
		t.Fatal(err)
	}

	_, err = evaluator.Evaluate(
		context.Background(),
		validRequest(),
		nil,
	)

	if !errors.Is(err, ErrNoPolicies) {
		t.Fatalf("expected ErrNoPolicies, got %v", err)
	}
}

func TestEvaluator_ShortCircuitsAfterRejection(t *testing.T) {
	enforcer := &fakeEnforcer{
		decisions: map[string]LimitDecision{
			"first": {
				PolicyID:   "first",
				Allowed:    false,
				Limit:      10,
				Remaining:  0,
				RetryAfter: 5,
			},
			"second": {
				PolicyID:  "second",
				Allowed:   true,
				Limit:     100,
				Remaining: 99,
			},
		},
	}

	evaluator, err := NewEvaluator(enforcer)
	if err != nil {
		t.Fatal(err)
	}

	decision, err := evaluator.Evaluate(
		context.Background(),
		validRequest(),
		[]Policy{
			validPolicy("first"),
			validPolicy("second"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if decision.Allowed {
		t.Fatal("expected request to be rejected")
	}

	if len(enforcer.calls) != 1 {
		t.Fatalf(
			"expected 1 enforcer call, got %d",
			len(enforcer.calls),
		)
	}

	if len(decision.Limits) != 1 {
		t.Fatalf(
			"expected 1 limit decision, got %d",
			len(decision.Limits),
		)
	}

	if decision.PolicyID != "first" {
		t.Fatalf(
			"expected rejecting policy first, got %q",
			decision.PolicyID,
		)
	}
}

func TestEvaluator_SkipsInapplicablePolicies(t *testing.T) {
	enforcer := &fakeEnforcer{
		decisions: map[string]LimitDecision{
			"applicable": {
				PolicyID:  "applicable",
				Allowed:   true,
				Limit:     10,
				Remaining: 9,
			},
		},
	}

	evaluator, err := NewEvaluator(enforcer)
	if err != nil {
		t.Fatal(err)
	}

	request := validRequest()

	policies := []Policy{
		{
			ID:              "wrong-tenant",
			Version:         1,
			Algorithm:       AlgorithmTokenBucket,
			Limit:           10,
			RefillRate:      1,
			RequestCost:     1,
			IdentityType:    request.Identity.Type,
			TenantID:        "tenant-other",
			Scope:           ScopeIdentity,
			EnforcementMode: EnforcementStrict,
			Enabled:         true,
			Priority:        10,
		},
		{
			ID:              "applicable",
			Version:         1,
			Algorithm:       AlgorithmTokenBucket,
			Limit:           10,
			RefillRate:      1,
			RequestCost:     1,
			IdentityType:    request.Identity.Type,
			TenantID:        request.TenantID,
			Scope:           ScopeIdentity,
			EnforcementMode: EnforcementStrict,
			Enabled:         true,
			Priority:        20,
		},
	}

	decision, err := evaluator.Evaluate(
		context.Background(),
		request,
		policies,
	)
	if err != nil {
		t.Fatal(err)
	}

	if !decision.Allowed {
		t.Fatal("expected request to be allowed")
	}

	if len(enforcer.calls) != 1 {
		t.Fatalf("expected 1 enforcer call, got %d", len(enforcer.calls))
	}

	if enforcer.calls[0] != "applicable" {
		t.Fatalf(
			"expected applicable policy to be enforced, got %q",
			enforcer.calls[0],
		)
	}
}

func TestEvaluator_EvaluatesPoliciesByPriority(t *testing.T) {
	enforcer := &fakeEnforcer{
		decisions: map[string]LimitDecision{
			"policy-a": {
				PolicyID:  "policy-a",
				Allowed:   true,
				Limit:     10,
				Remaining: 9,
			},
			"policy-b": {
				PolicyID:  "policy-b",
				Allowed:   true,
				Limit:     20,
				Remaining: 19,
			},
		},
	}

	evaluator, err := NewEvaluator(enforcer)
	if err != nil {
		t.Fatal(err)
	}

	request := validRequest()

	policyA := validPolicy("policy-a")
	policyA.Priority = 20

	policyB := validPolicy("policy-b")
	policyB.Priority = 10

	decision, err := evaluator.Evaluate(
		context.Background(),
		request,
		[]Policy{policyA, policyB},
	)
	if err != nil {
		t.Fatal(err)
	}

	if !decision.Allowed {
		t.Fatal("expected request to be allowed")
	}

	if len(enforcer.calls) != 2 {
		t.Fatalf("expected 2 enforcer calls, got %d", len(enforcer.calls))
	}

	if enforcer.calls[0] != "policy-b" {
		t.Fatalf(
			"expected policy-b first, got %q",
			enforcer.calls[0],
		)
	}

	if enforcer.calls[1] != "policy-a" {
		t.Fatalf(
			"expected policy-a second, got %q",
			enforcer.calls[1],
		)
	}

	if len(decision.Limits) != 2 {
		t.Fatalf(
			"expected 2 limit decisions, got %d",
			len(decision.Limits),
		)
	}
}

func TestEvaluator_UsesPolicyIDAsPriorityTieBreaker(t *testing.T) {
	enforcer := &fakeEnforcer{
		decisions: map[string]LimitDecision{
			"policy-a": {
				PolicyID:  "policy-a",
				Allowed:   true,
				Limit:     10,
				Remaining: 9,
			},
			"policy-b": {
				PolicyID:  "policy-b",
				Allowed:   true,
				Limit:     20,
				Remaining: 19,
			},
		},
	}

	evaluator, err := NewEvaluator(enforcer)
	if err != nil {
		t.Fatal(err)
	}

	request := validRequest()

	policyA := validPolicy("policy-a")
	policyA.Priority = 10

	policyB := validPolicy("policy-b")
	policyB.Priority = 10

	_, err = evaluator.Evaluate(
		context.Background(),
		request,
		[]Policy{policyB, policyA},
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(enforcer.calls) != 2 {
		t.Fatalf("expected 2 enforcer calls, got %d", len(enforcer.calls))
	}

	if enforcer.calls[0] != "policy-a" {
		t.Fatalf(
			"expected policy-a first, got %q",
			enforcer.calls[0],
		)
	}

	if enforcer.calls[1] != "policy-b" {
		t.Fatalf(
			"expected policy-b second, got %q",
			enforcer.calls[1],
		)
	}
}
