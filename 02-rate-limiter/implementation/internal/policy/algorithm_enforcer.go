package policy

import (
	"context"
	"errors"
)

var (
	ErrUnsupportedAlgorithm = errors.New("unsupported rate-limit algorithm")
)

type AlgorithmEnforcer struct {
	enforcers map[Algorithm]Enforcer
}

func NewAlgorithmEnforcer(
	enforcers map[Algorithm]Enforcer,
) *AlgorithmEnforcer {
	copied := make(map[Algorithm]Enforcer, len(enforcers))

	for algorithm, enforcer := range enforcers {
		copied[algorithm] = enforcer
	}

	return &AlgorithmEnforcer{
		enforcers: copied,
	}
}

func (e *AlgorithmEnforcer) Allow(
	ctx context.Context,
	key string,
	policy Policy,
	cost int64,
) (LimitDecision, error) {
	enforcer, ok := e.enforcers[policy.Algorithm]
	if !ok {
		return LimitDecision{}, ErrUnsupportedAlgorithm
	}

	return enforcer.Allow(ctx, key, policy, cost)
}
