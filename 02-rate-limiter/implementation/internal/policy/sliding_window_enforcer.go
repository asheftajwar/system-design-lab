package policy

import (
	"context"
	"errors"

	"github.com/asheftajwar/system-design-lab/02-rate-limiter/internal/limiter"
)

var ErrWrongSlidingWindowAlgorithm = errors.New(
	"policy algorithm must be sliding_window_log",
)

type SlidingWindowEnforcer struct {
	limiter *limiter.RedisSlidingWindow
}

func NewSlidingWindowEnforcer(
	l *limiter.RedisSlidingWindow,
) *SlidingWindowEnforcer {
	return &SlidingWindowEnforcer{
		limiter: l,
	}
}

func (e *SlidingWindowEnforcer) Allow(
	ctx context.Context,
	key string,
	policy Policy,
	cost int64,
) (LimitDecision, error) {
	if policy.Algorithm != AlgorithmSlidingWindow {
		return LimitDecision{}, ErrWrongSlidingWindowAlgorithm
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
