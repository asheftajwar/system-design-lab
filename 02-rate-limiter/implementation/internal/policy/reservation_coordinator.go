package policy

import (
	"context"
	"errors"
	"time"
)

const reservationCleanupTimeout = 2 * time.Second

var (
	ErrReservationRejected = errors.New("reservation rejected")
	ErrReservationFailed   = errors.New("reservation failed")
)

type ReservationCoordinator struct {
	enforcer ReservingEnforcer
}

// ReservationTransaction coordinates multiple reservations.
//
// Commit and Abort support sequential retries after failures.
// A transaction must not be operated on concurrently.
type ReservationTransaction struct {
	reservations    []Reservation
	decisions       []LimitDecision
	state           reservationTransactionState
	nextCommitIndex int
	nextAbortIndex  int
	commitStarted   bool
	abortStarted    bool
}

type reservationTransactionState uint8

const (
	reservationTransactionOpen reservationTransactionState = iota
	reservationTransactionCommitted
	reservationTransactionAborted
)

func NewReservationCoordinator(
	enforcer ReservingEnforcer,
) (*ReservationCoordinator, error) {
	if enforcer == nil {
		return nil, errors.New("reserving enforcer is required")
	}

	return &ReservationCoordinator{
		enforcer: enforcer,
	}, nil
}

func newReservationCleanupContext(
	ctx context.Context,
) (context.Context, context.CancelFunc) {
	return context.WithTimeout(
		context.WithoutCancel(ctx),
		reservationCleanupTimeout,
	)
}

func (c *ReservationCoordinator) Reserve(
	ctx context.Context,
	request Request,
	policies []Policy,
) (*ReservationTransaction, error) {
	applicable := applicablePolicies(request, policies)

	if len(applicable) == 0 {
		return nil, ErrNoPolicies
	}

	reservations := make([]Reservation, 0, len(applicable))
	decisions := make([]LimitDecision, 0, len(applicable))

	for _, policy := range applicable {

		if err := policy.Validate(); err != nil {
			return nil, errors.Join(err, c.abortAll(ctx, reservations))
		}

		key, err := buildKey(request, policy)
		if err != nil {
			return nil, errors.Join(
				ErrReservationFailed,
				err,
				c.abortAll(ctx, reservations),
			)
		}

		decision, reservation, err := c.enforcer.Reserve(
			ctx,
			key,
			policy,
			request.Cost,
		)

		if err != nil {
			return nil, errors.Join(
				ErrReservationFailed,
				err,
				c.abortAll(ctx, reservations, reservation),
			)
		}

		decisions = append(decisions, decision)

		if !decision.Allowed {
			return nil, errors.Join(
				ErrReservationRejected,
				c.abortAll(ctx, reservations, reservation),
			)
		}

		if reservation == nil {
			return nil, errors.Join(
				errors.New("enforcer returned nil reservation for allowed decision"),
				c.abortAll(ctx, reservations),
			)
		}

		reservations = append(reservations, reservation)
	}

	return &ReservationTransaction{
		reservations: reservations,
		decisions:    decisions,
		state:        reservationTransactionOpen,
	}, nil
}

func (c *ReservationCoordinator) abortAll(
	ctx context.Context,
	reservations []Reservation,
	additional ...Reservation,
) error {
	cleanupCtx, cancel := newReservationCleanupContext(ctx)
	defer cancel()

	var abortErr error

	for _, reservation := range additional {
		if reservation == nil {
			continue
		}

		if err := reservation.Abort(cleanupCtx); err != nil {
			abortErr = errors.Join(abortErr, err)
		}
	}

	for i := len(reservations) - 1; i >= 0; i-- {
		if err := reservations[i].Abort(cleanupCtx); err != nil {
			abortErr = errors.Join(abortErr, err)
		}
	}

	return abortErr
}

func (t *ReservationTransaction) Decisions() []LimitDecision {
	return append([]LimitDecision(nil), t.decisions...)
}

func (t *ReservationTransaction) Commit(
	ctx context.Context,
) error {

	if t.abortStarted || t.state != reservationTransactionOpen {
		return ErrReservationClosed
	}

	t.commitStarted = true

	for t.nextCommitIndex < len(t.reservations) {
		reservation := t.reservations[t.nextCommitIndex]

		if err := reservation.Commit(ctx); err != nil {
			return err
		}

		t.nextCommitIndex++
	}

	t.state = reservationTransactionCommitted
	return nil
}

func (t *ReservationTransaction) Abort(
	ctx context.Context,
) error {
	if t.commitStarted || t.state != reservationTransactionOpen {
		return ErrReservationClosed
	}

	t.abortStarted = true

	cleanupCtx, cancel := newReservationCleanupContext(ctx)
	defer cancel()

	for t.nextAbortIndex < len(t.reservations) {
		index := len(t.reservations) - 1 - t.nextAbortIndex

		if err := t.reservations[index].Abort(cleanupCtx); err != nil {
			return err
		}

		t.nextAbortIndex++
	}

	t.state = reservationTransactionAborted
	return nil
}
