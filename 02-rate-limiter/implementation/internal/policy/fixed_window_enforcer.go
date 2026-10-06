package policy

import (
	"context"
	"errors"

	"github.com/asheftajwar/system-design-lab/02-rate-limiter/internal/limiter"
)

var ErrWrongFixedWindowAlgorithm = errors.New(
	"policy algorithm must be fixed_window",
)

type FixedWindowEnforcer struct {
	limiter *limiter.RedisFixedWindow
}

func NewFixedWindowEnforcer(
	l *limiter.RedisFixedWindow,
) *FixedWindowEnforcer {
	return &FixedWindowEnforcer{
		limiter: l,
	}
}

func (e *FixedWindowEnforcer) Allow(
	ctx context.Context,
	key string,
	policy Policy,
	cost int64,
) (LimitDecision, error) {
	if policy.Algorithm != AlgorithmFixedWindow {
		return LimitDecision{}, ErrWrongFixedWindowAlgorithm
	}

	decision, err := e.limiter.Allow(
		ctx,
		key,
		policy.Limit,
		policy.WindowSeconds,
		cost,
	)
	if err != nil {
		return LimitDecision{}, err
	}

	return LimitDecision{
		PolicyID:      policy.ID,
		PolicyVersion: policy.Version,
		Allowed:       decision.Allowed,
		Limit:         decision.Limit,
		Remaining:     decision.Remaining,
		RetryAfter:    decision.RetryAfter,
		ResetAfter:    decision.ResetAfter,
	}, nil
}
