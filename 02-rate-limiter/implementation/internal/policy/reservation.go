package policy

import (
	"context"
	"errors"
)

var ErrReservationClosed = errors.New("reservation is closed")

// Reservation represents temporarily held quota.
//
// Implementations must support retrying Commit or Abort after an error.
// An error does not necessarily mean that the underlying operation was
// not applied; implementations should handle repeated operations safely.
type Reservation interface {
	Commit(ctx context.Context) error
	Abort(ctx context.Context) error
}

type ReservingEnforcer interface {
	Reserve(
		ctx context.Context,
		key string,
		policy Policy,
		cost int64,
	) (LimitDecision, Reservation, error)
}
