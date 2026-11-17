package policy

import (
	"context"
	"errors"
	"testing"
)

type fakeReservingEnforcer struct {
	reserveFunc func(
		context.Context,
		string,
		Policy,
		int64,
	) (LimitDecision, Reservation, error)

	keys []string
}

type retryableReservation struct {
	commitCalls int
	abortCalls  int

	commitErrors []error
	abortErrors  []error
}

func (f *fakeReservingEnforcer) Reserve(
	ctx context.Context,
	key string,
	policy Policy,
	cost int64,
) (LimitDecision, Reservation, error) {
	f.keys = append(f.keys, key)

	return f.reserveFunc(ctx, key, policy, cost)
}

type recordingReservation struct {
	commits int
	aborts  int

	commitErr error
	abortErr  error

	abortContexts []context.Context
}

func (r *recordingReservation) Commit(context.Context) error {
	r.commits++
	return r.commitErr
}

type reservationContextKey struct{}

func (r *recordingReservation) Abort(ctx context.Context) error {
	r.aborts++
	r.abortContexts = append(r.abortContexts, ctx)

	if err := ctx.Err(); err != nil {
		return errors.New("reservation received canceled cleanup context")
	}

	if _, ok := ctx.Deadline(); !ok {
		return errors.New("reservation received cleanup context without deadline")
	}

	return r.abortErr
}

func validReservationPolicy(id string, priority int) Policy {
	return Policy{
		ID:              id,
		Version:         1,
		Name:            id,
		Algorithm:       AlgorithmTokenBucket,
		Limit:           10,
		RefillRate:      1,
		RequestCost:     1,
		IdentityType:    IdentityUser,
		TenantID:        "tenant-1",
		Priority:        priority,
		Scope:           ScopeTenant,
		EnforcementMode: EnforcementStrict,
		Enabled:         true,
	}
}

func validReservationRequest() Request {
	return Request{
		Identity: Identity{
			Type:  IdentityUser,
			Value: "user-1",
		},
		TenantID: "tenant-1",
		Resource: Resource{
			Method: "GET",
			Path:   "/users",
		},
		Cost: 1,
	}
}

func TestReservationCoordinator_ReservesAllPolicies(t *testing.T) {
	r1 := &recordingReservation{}
	r2 := &recordingReservation{}

	call := 0

	enforcer := &fakeReservingEnforcer{
		reserveFunc: func(
			context.Context,
			string,
			Policy,
			int64,
		) (LimitDecision, Reservation, error) {
			call++

			switch call {
			case 1:
				return LimitDecision{
					PolicyID: "p1",
					Allowed:  true,
					Limit:    10,
				}, r1, nil
			case 2:
				return LimitDecision{
					PolicyID: "p2",
					Allowed:  true,
					Limit:    20,
				}, r2, nil
			default:
				t.Fatalf("unexpected reservation call")
				return LimitDecision{}, nil, nil
			}
		},
	}

	coordinator, err := NewReservationCoordinator(enforcer)
	if err != nil {
		t.Fatalf("constructor failed: %v", err)
	}

	tx, err := coordinator.Reserve(
		context.Background(),
		validReservationRequest(),
		[]Policy{
			validReservationPolicy("p1", 1),
			validReservationPolicy("p2", 2),
		},
	)
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	if len(tx.Decisions()) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(tx.Decisions()))
	}

	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	if r1.commits != 1 || r2.commits != 1 {
		t.Fatalf(
			"expected both reservations committed once, got r1=%d r2=%d",
			r1.commits,
			r2.commits,
		)
	}
}

func TestReservationCoordinator_RejectsAndAbortsPreviousReservations(t *testing.T) {
	r1 := &recordingReservation{}

	call := 0

	enforcer := &fakeReservingEnforcer{
		reserveFunc: func(
			context.Context,
			string,
			Policy,
			int64,
		) (LimitDecision, Reservation, error) {
			call++

			if call == 1 {
				return LimitDecision{
					PolicyID: "p1",
					Allowed:  true,
				}, r1, nil
			}

			return LimitDecision{
				PolicyID:   "p2",
				Allowed:    false,
				RetryAfter: 5,
			}, &recordingReservation{}, nil
		},
	}

	coordinator, err := NewReservationCoordinator(enforcer)
	if err != nil {
		t.Fatalf("constructor failed: %v", err)
	}

	_, err = coordinator.Reserve(
		context.Background(),
		validReservationRequest(),
		[]Policy{
			validReservationPolicy("p1", 1),
			validReservationPolicy("p2", 2),
		},
	)

	if !errors.Is(err, ErrReservationRejected) {
		t.Fatalf("expected ErrReservationRejected, got %v", err)
	}

	if r1.aborts != 1 {
		t.Fatalf("expected previous reservation to abort once, got %d", r1.aborts)
	}
}

func TestReservationCoordinator_AbortsOnEnforcerError(t *testing.T) {
	r1 := &recordingReservation{}
	expectedErr := errors.New("redis unavailable")

	call := 0

	enforcer := &fakeReservingEnforcer{
		reserveFunc: func(
			context.Context,
			string,
			Policy,
			int64,
		) (LimitDecision, Reservation, error) {
			call++

			if call == 1 {
				return LimitDecision{
					PolicyID: "p1",
					Allowed:  true,
				}, r1, nil
			}

			return LimitDecision{}, nil, expectedErr
		},
	}

	coordinator, err := NewReservationCoordinator(enforcer)
	if err != nil {
		t.Fatalf("constructor failed: %v", err)
	}

	_, err = coordinator.Reserve(
		context.Background(),
		validReservationRequest(),
		[]Policy{
			validReservationPolicy("p1", 1),
			validReservationPolicy("p2", 2),
		},
	)

	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected underlying error, got %v", err)
	}

	if r1.aborts != 1 {
		t.Fatalf("expected reservation to abort once, got %d", r1.aborts)
	}
}

func TestReservationTransaction_AbortIsReverseOrder(t *testing.T) {
	var order []string

	tx := &ReservationTransaction{
		reservations: []Reservation{
			&orderedReservation{
				name:  "p1",
				order: &order,
			},
			&orderedReservation{
				name:  "p2",
				order: &order,
			},
		},
		state: reservationTransactionOpen,
	}

	if err := tx.Abort(context.Background()); err != nil {
		t.Fatalf("abort failed: %v", err)
	}

	if len(order) != 2 || order[0] != "p2" || order[1] != "p1" {
		t.Fatalf("unexpected abort order: %v", order)
	}
}

type orderedReservation struct {
	name  string
	order *[]string
}

func (r *orderedReservation) Commit(context.Context) error {
	return nil
}

func (r *orderedReservation) Abort(context.Context) error {
	*r.order = append(*r.order, r.name)
	return nil
}

func TestReservationTransaction_CannotCommitAfterAbort(t *testing.T) {
	tx := &ReservationTransaction{
		reservations: []Reservation{
			&recordingReservation{},
		},
		state: reservationTransactionOpen,
	}

	if err := tx.Abort(context.Background()); err != nil {
		t.Fatalf("abort failed: %v", err)
	}

	if err := tx.Commit(context.Background()); !errors.Is(err, ErrReservationClosed) {
		t.Fatalf("expected ErrReservationClosed, got %v", err)
	}
}

func TestReservationTransaction_CannotAbortAfterCommit(t *testing.T) {
	r := &recordingReservation{}

	tx := &ReservationTransaction{
		reservations: []Reservation{r},
		state:        reservationTransactionOpen,
	}

	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	if err := tx.Abort(context.Background()); !errors.Is(err, ErrReservationClosed) {
		t.Fatalf("expected ErrReservationClosed, got %v", err)
	}
}

func (r *retryableReservation) Commit(context.Context) error {
	r.commitCalls++

	if len(r.commitErrors) == 0 {
		return nil
	}

	index := r.commitCalls - 1
	if index < len(r.commitErrors) {
		return r.commitErrors[index]
	}

	return nil
}

func (r *retryableReservation) Abort(context.Context) error {
	r.abortCalls++

	if len(r.abortErrors) == 0 {
		return nil
	}

	index := r.abortCalls - 1
	if index < len(r.abortErrors) {
		return r.abortErrors[index]
	}

	return nil
}

func TestReservationTransaction_PartialCommitRecovery(t *testing.T) {
	r1 := &retryableReservation{}
	r2 := &retryableReservation{
		commitErrors: []error{
			errors.New("temporary commit failure"),
		},
	}
	r3 := &retryableReservation{}

	tx := &ReservationTransaction{
		reservations: []Reservation{r1, r2, r3},
		state:        reservationTransactionOpen,
	}

	// First commit: r1 succeeds, r2 fails.
	if err := tx.Commit(context.Background()); err == nil {
		t.Fatal("expected first commit to fail")
	}

	if r1.commitCalls != 1 {
		t.Fatalf("expected r1 commit once, got %d", r1.commitCalls)
	}

	if r2.commitCalls != 1 {
		t.Fatalf("expected r2 commit once, got %d", r2.commitCalls)
	}

	if r3.commitCalls != 0 {
		t.Fatalf("expected r3 not to be committed, got %d", r3.commitCalls)
	}

	// Second commit: resume from r2.
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}

	if r1.commitCalls != 1 {
		t.Fatalf("r1 must not be committed again, got %d calls", r1.commitCalls)
	}

	if r2.commitCalls != 2 {
		t.Fatalf("expected r2 commit twice, got %d", r2.commitCalls)
	}

	if r3.commitCalls != 1 {
		t.Fatalf("expected r3 commit once, got %d", r3.commitCalls)
	}

	if tx.state != reservationTransactionCommitted {
		t.Fatal("expected transaction to be committed")
	}
}

func TestReservationTransaction_PartialAbortRecovery(t *testing.T) {
	r1 := &retryableReservation{}
	r2 := &retryableReservation{}
	r3 := &retryableReservation{
		abortErrors: []error{
			errors.New("temporary abort failure"),
		},
	}

	tx := &ReservationTransaction{
		reservations: []Reservation{r1, r2, r3},
		state:        reservationTransactionOpen,
	}

	// First abort: r3 fails.
	if err := tx.Abort(context.Background()); err == nil {
		t.Fatal("expected first abort to fail")
	}

	if r3.abortCalls != 1 {
		t.Fatalf("expected r3 abort once, got %d", r3.abortCalls)
	}

	if r2.abortCalls != 0 || r1.abortCalls != 0 {
		t.Fatal("abort should stop after the first failure")
	}

	// Second abort: resume from r3, then continue in reverse order.
	if err := tx.Abort(context.Background()); err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}

	if r3.abortCalls != 2 {
		t.Fatalf("expected r3 abort twice, got %d", r3.abortCalls)
	}

	if r2.abortCalls != 1 || r1.abortCalls != 1 {
		t.Fatalf(
			"expected r2 and r1 aborted once, got r2=%d r1=%d",
			r2.abortCalls,
			r1.abortCalls,
		)
	}

	if tx.state != reservationTransactionAborted {
		t.Fatal("expected transaction to be aborted")
	}
}

func TestReservationTransaction_CannotAbortAfterFailedCommit(t *testing.T) {
	expectedErr := errors.New("commit failure")

	r := &retryableReservation{
		commitErrors: []error{expectedErr},
	}

	tx := &ReservationTransaction{
		reservations: []Reservation{r},
		state:        reservationTransactionOpen,
	}

	if err := tx.Commit(context.Background()); !errors.Is(err, expectedErr) {
		t.Fatalf("expected commit failure, got %v", err)
	}

	if err := tx.Abort(context.Background()); !errors.Is(err, ErrReservationClosed) {
		t.Fatalf("expected ErrReservationClosed, got %v", err)
	}

	if r.abortCalls != 0 {
		t.Fatalf("abort must not run after commit started, got %d calls", r.abortCalls)
	}
}

func TestReservationTransaction_CannotCommitAfterFailedAbort(t *testing.T) {
	expectedErr := errors.New("abort failure")

	r := &retryableReservation{
		abortErrors: []error{expectedErr},
	}

	tx := &ReservationTransaction{
		reservations: []Reservation{r},
		state:        reservationTransactionOpen,
	}

	if err := tx.Abort(context.Background()); !errors.Is(err, expectedErr) {
		t.Fatalf("expected abort failure, got %v", err)
	}

	if err := tx.Commit(context.Background()); !errors.Is(err, ErrReservationClosed) {
		t.Fatalf("expected ErrReservationClosed, got %v", err)
	}

	if r.commitCalls != 0 {
		t.Fatalf("commit must not run after abort started, got %d calls", r.commitCalls)
	}
}

func TestReservationCoordinator_EnforcerErrorPreservesCleanupError(t *testing.T) {
	cleanupErr := errors.New("abort cleanup failed")
	enforcerErr := errors.New("redis unavailable")

	r1 := &recordingReservation{
		abortErr: cleanupErr,
	}

	call := 0

	enforcer := &fakeReservingEnforcer{
		reserveFunc: func(
			context.Context,
			string,
			Policy,
			int64,
		) (LimitDecision, Reservation, error) {
			call++

			if call == 1 {
				return LimitDecision{
					PolicyID: "p1",
					Allowed:  true,
				}, r1, nil
			}

			return LimitDecision{}, nil, enforcerErr
		},
	}

	coordinator, err := NewReservationCoordinator(enforcer)
	if err != nil {
		t.Fatalf("constructor failed: %v", err)
	}

	_, err = coordinator.Reserve(
		context.Background(),
		validReservationRequest(),
		[]Policy{
			validReservationPolicy("p1", 1),
			validReservationPolicy("p2", 2),
		},
	)

	if !errors.Is(err, ErrReservationFailed) {
		t.Fatalf("expected ErrReservationFailed, got %v", err)
	}

	if !errors.Is(err, enforcerErr) {
		t.Fatalf("expected enforcer error, got %v", err)
	}

	if !errors.Is(err, cleanupErr) {
		t.Fatalf("expected cleanup error, got %v", err)
	}
}

func TestReservationCoordinator_RejectionPreservesCleanupError(t *testing.T) {
	cleanupErr := errors.New("abort cleanup failed")

	r1 := &recordingReservation{
		abortErr: cleanupErr,
	}

	call := 0

	enforcer := &fakeReservingEnforcer{
		reserveFunc: func(
			context.Context,
			string,
			Policy,
			int64,
		) (LimitDecision, Reservation, error) {
			call++

			if call == 1 {
				return LimitDecision{
					PolicyID: "p1",
					Allowed:  true,
				}, r1, nil
			}

			return LimitDecision{
				PolicyID: "p2",
				Allowed:  false,
			}, nil, nil
		},
	}

	coordinator, err := NewReservationCoordinator(enforcer)
	if err != nil {
		t.Fatalf("constructor failed: %v", err)
	}

	_, err = coordinator.Reserve(
		context.Background(),
		validReservationRequest(),
		[]Policy{
			validReservationPolicy("p1", 1),
			validReservationPolicy("p2", 2),
		},
	)

	if !errors.Is(err, ErrReservationRejected) {
		t.Fatalf("expected ErrReservationRejected, got %v", err)
	}

	if !errors.Is(err, cleanupErr) {
		t.Fatalf("expected cleanup error, got %v", err)
	}
}

func TestReservationCoordinator_RejectedReservationAbortError(t *testing.T) {
	cleanupErr := errors.New("rejected reservation abort failed")

	rejectedReservation := &recordingReservation{
		abortErr: cleanupErr,
	}

	enforcer := &fakeReservingEnforcer{
		reserveFunc: func(
			context.Context,
			string,
			Policy,
			int64,
		) (LimitDecision, Reservation, error) {
			return LimitDecision{
				PolicyID: "p1",
				Allowed:  false,
			}, rejectedReservation, nil
		},
	}

	coordinator, err := NewReservationCoordinator(enforcer)
	if err != nil {
		t.Fatalf("constructor failed: %v", err)
	}

	_, err = coordinator.Reserve(
		context.Background(),
		validReservationRequest(),
		[]Policy{validReservationPolicy("p1", 1)},
	)

	if !errors.Is(err, ErrReservationRejected) {
		t.Fatalf("expected ErrReservationRejected, got %v", err)
	}

	if !errors.Is(err, cleanupErr) {
		t.Fatalf("expected cleanup error, got %v", err)
	}

	if rejectedReservation.aborts != 1 {
		t.Fatalf(
			"expected rejected reservation abort once, got %d",
			rejectedReservation.aborts,
		)
	}
}

func TestReservationCoordinator_RollbackSurvivesRequestCancellation(t *testing.T) {
	r1 := &recordingReservation{}

	type contextKey string
	const expectedValue = "trace-123"

	baseCtx := context.WithValue(
		context.Background(),
		reservationContextKey{},
		expectedValue,
	)
	ctx, cancel := context.WithCancel(baseCtx)
	defer cancel()

	call := 0
	enforcerErr := errors.New("second reservation failed")

	enforcer := &fakeReservingEnforcer{
		reserveFunc: func(
			context.Context,
			string,
			Policy,
			int64,
		) (LimitDecision, Reservation, error) {
			call++

			if call == 1 {
				return LimitDecision{
					PolicyID: "p1",
					Allowed:  true,
				}, r1, nil
			}

			// Simulate cancellation while a later reservation fails.
			cancel()
			return LimitDecision{}, nil, enforcerErr
		},
	}

	coordinator, err := NewReservationCoordinator(enforcer)
	if err != nil {
		t.Fatalf("constructor failed: %v", err)
	}

	_, err = coordinator.Reserve(
		ctx,
		validReservationRequest(),
		[]Policy{
			validReservationPolicy("p1", 1),
			validReservationPolicy("p2", 2),
		},
	)

	if !errors.Is(err, ErrReservationFailed) {
		t.Fatalf("expected ErrReservationFailed, got %v", err)
	}

	if !errors.Is(err, enforcerErr) {
		t.Fatalf("expected enforcer error, got %v", err)
	}

	if r1.aborts != 1 {
		t.Fatalf("expected one rollback, got %d", r1.aborts)
	}

	if len(r1.abortContexts) != 1 {
		t.Fatalf(
			"expected one recorded cleanup context, got %d",
			len(r1.abortContexts),
		)
	}

	if got := r1.abortContexts[0].Value(reservationContextKey{}); got != expectedValue {
		t.Fatalf("context value = %v, want %v", got, expectedValue)
	}
}

func TestReservationTransaction_AbortSurvivesCallerCancellation(t *testing.T) {
	const expectedValue = "trace-456"

	baseCtx := context.WithValue(
		context.Background(),
		reservationContextKey{},
		expectedValue,
	)
	ctx, cancel := context.WithCancel(baseCtx)
	cancel()

	r := &recordingReservation{}

	tx := &ReservationTransaction{
		reservations: []Reservation{r},
		state:        reservationTransactionOpen,
	}

	if err := tx.Abort(ctx); err != nil {
		t.Fatalf("abort should survive caller cancellation: %v", err)
	}

	if r.aborts != 1 {
		t.Fatalf("expected one abort, got %d", r.aborts)
	}

	if len(r.abortContexts) != 1 {
		t.Fatalf(
			"expected one recorded cleanup context, got %d",
			len(r.abortContexts),
		)
	}

	if got := r.abortContexts[0].Value(reservationContextKey{}); got != expectedValue {
		t.Fatalf("context value = %v, want %v", got, expectedValue)
	}

	if tx.state != reservationTransactionAborted {
		t.Fatal("expected transaction to be aborted")
	}
}

func TestReservationCoordinator_AbortsReservationReturnedWithEnforcerError(
	t *testing.T,
) {
	previousReservation := &recordingReservation{}
	returnedReservation := &recordingReservation{}
	expectedErr := errors.New("ambiguous reservation result")

	call := 0

	enforcer := &fakeReservingEnforcer{
		reserveFunc: func(
			context.Context,
			string,
			Policy,
			int64,
		) (LimitDecision, Reservation, error) {
			call++

			if call == 1 {
				return LimitDecision{
					PolicyID: "p1",
					Allowed:  true,
				}, previousReservation, nil
			}

			return LimitDecision{}, returnedReservation, expectedErr
		},
	}

	coordinator, err := NewReservationCoordinator(enforcer)
	if err != nil {
		t.Fatalf("constructor failed: %v", err)
	}

	_, err = coordinator.Reserve(
		context.Background(),
		validReservationRequest(),
		[]Policy{
			validReservationPolicy("p1", 1),
			validReservationPolicy("p2", 2),
		},
	)

	if !errors.Is(err, ErrReservationFailed) {
		t.Fatalf("expected ErrReservationFailed, got %v", err)
	}

	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected underlying enforcer error, got %v", err)
	}

	if previousReservation.aborts != 1 {
		t.Fatalf(
			"expected previous reservation aborted once, got %d",
			previousReservation.aborts,
		)
	}

	if returnedReservation.aborts != 1 {
		t.Fatalf(
			"expected returned reservation aborted once, got %d",
			returnedReservation.aborts,
		)
	}
}
