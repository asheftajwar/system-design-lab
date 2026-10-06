package policy

import (
	"context"
	"errors"

	"github.com/asheftajwar/system-design-lab/02-rate-limiter/internal/limiter"
)

type TokenBucketEnforcer struct {
	limiter *limiter.RedisTokenBucket
}

func NewTokenBucketEnforcer(
	redisLimiter *limiter.RedisTokenBucket,
) (*TokenBucketEnforcer, error) {
	if redisLimiter == nil {
		return nil, errors.New("redis token bucket is required")
	}

	return &TokenBucketEnforcer{
		limiter: redisLimiter,
	}, nil
}

func (e *TokenBucketEnforcer) Allow(
	ctx context.Context,
	key string,
	policy Policy,
	cost int64,
) (LimitDecision, error) {
	if policy.Algorithm != AlgorithmTokenBucket {
		return LimitDecision{}, ErrInvalidAlgorithm
	}

	decision, err := e.limiter.Allow(
		ctx,
		key,
		policy.Limit,
		policy.RefillRate,
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
