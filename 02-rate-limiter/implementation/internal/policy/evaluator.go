package policy

import (
	"context"
	"errors"
	"sort"
)

type Request struct {
	Identity Identity
	TenantID string
	Resource Resource
	Cost     int64
}

type LimitDecision struct {
	PolicyID      string
	PolicyVersion int64
	Allowed       bool
	Limit         int64
	Remaining     int64
	RetryAfter    int64
	ResetAfter    int64
}

type Enforcer interface {
	Allow(
		ctx context.Context,
		key string,
		policy Policy,
		cost int64,
	) (LimitDecision, error)
}

type Decision struct {
	Allowed       bool
	Limit         int64
	Remaining     int64
	RetryAfter    int64
	ResetAfter    int64
	PolicyID      string
	PolicyVersion int64
	Limits        []LimitDecision
}

var (
	ErrInvalidRequest = errors.New("invalid rate-limit request")
	ErrNoPolicies     = errors.New("no applicable policies")
)

type Evaluator struct {
	enforcer Enforcer
}

func applicablePolicies(request Request, policies []Policy) []Policy {
	applicable := make([]Policy, 0, len(policies))

	for _, policy := range policies {
		if AppliesTo(request, policy) {
			applicable = append(applicable, policy)
		}
	}

	sort.SliceStable(applicable, func(i, j int) bool {
		if applicable[i].Priority != applicable[j].Priority {
			return applicable[i].Priority < applicable[j].Priority
		}

		return applicable[i].ID < applicable[j].ID
	})

	return applicable
}

func NewEvaluator(enforcer Enforcer) (*Evaluator, error) {
	if enforcer == nil {
		return nil, errors.New("enforcer is required")
	}

	return &Evaluator{
		enforcer: enforcer,
	}, nil
}

func (r Request) Validate() error {
	if err := r.Identity.Validate(); err != nil {
		return err
	}

	if r.TenantID == "" {
		return ErrInvalidTenantID
	}

	if err := r.Resource.Validate(); err != nil {
		return err
	}

	if r.Cost <= 0 {
		return ErrInvalidRequest
	}

	return nil
}

func (e *Evaluator) Evaluate(
	ctx context.Context,
	request Request,
	policies []Policy,
) (Decision, error) {
	if err := request.Validate(); err != nil {
		return Decision{}, err
	}

	policies = applicablePolicies(request, policies)

	if len(policies) == 0 {
		return Decision{}, ErrNoPolicies
	}

	decision := Decision{
		Allowed: true,
		Limits:  make([]LimitDecision, 0, len(policies)),
	}

	for _, policy := range policies {
		if !policy.Enabled {
			continue
		}

		if err := policy.Validate(); err != nil {
			return Decision{}, err
		}

		key, err := buildKey(request, policy)
		if err != nil {
			return Decision{}, err
		}

		limitDecision, err := e.enforcer.Allow(
			ctx,
			key,
			policy,
			request.Cost,
		)
		if err != nil {
			return Decision{}, err
		}

		decision.Limits = append(
			decision.Limits,
			limitDecision,
		)

		if !limitDecision.Allowed {
			decision.Allowed = false
			decision.PolicyID = limitDecision.PolicyID
			decision.PolicyVersion = limitDecision.PolicyVersion
			decision.Limit = limitDecision.Limit
			decision.Remaining = limitDecision.Remaining
			decision.RetryAfter = limitDecision.RetryAfter
			decision.ResetAfter = limitDecision.ResetAfter

			return decision, nil
		}
	}

	if len(decision.Limits) == 0 {
		return Decision{}, ErrNoPolicies
	}

	if decision.Allowed {
		decision.PolicyID = decision.Limits[0].PolicyID
		decision.PolicyVersion = decision.Limits[0].PolicyVersion
		decision.Limit = decision.Limits[0].Limit
		decision.Remaining = decision.Limits[0].Remaining
		decision.RetryAfter = decision.Limits[0].RetryAfter
		decision.ResetAfter = decision.Limits[0].ResetAfter
	}

	return decision, nil
}
