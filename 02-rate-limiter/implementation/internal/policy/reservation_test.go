package policy

import (
	"context"
	"errors"
	"testing"
)

type fakeReservation struct {
	committed bool
	aborted   bool
}

func (r *fakeReservation) Commit(context.Context) error {
	if r.committed || r.aborted {
		return ErrReservationClosed
	}

	r.committed = true
	return nil
}

func (r *fakeReservation) Abort(context.Context) error {
	if r.committed || r.aborted {
		return ErrReservationClosed
	}

	r.aborted = true
	return nil
}

func TestReservation_CommitOnce(t *testing.T) {
	r := &fakeReservation{}

	if err := r.Commit(context.Background()); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	if !r.committed {
		t.Fatal("expected reservation to be committed")
	}
}

func TestReservation_CommitTwice(t *testing.T) {
	r := &fakeReservation{}

	if err := r.Commit(context.Background()); err != nil {
		t.Fatalf("first commit failed: %v", err)
	}

	if err := r.Commit(context.Background()); !errors.Is(err, ErrReservationClosed) {
		t.Fatalf("expected ErrReservationClosed, got %v", err)
	}
}

func TestReservation_AbortOnce(t *testing.T) {
	r := &fakeReservation{}

	if err := r.Abort(context.Background()); err != nil {
		t.Fatalf("abort failed: %v", err)
	}

	if !r.aborted {
		t.Fatal("expected reservation to be aborted")
	}
}

func TestReservation_AbortTwice(t *testing.T) {
	r := &fakeReservation{}

	if err := r.Abort(context.Background()); err != nil {
		t.Fatalf("first abort failed: %v", err)
	}

	if err := r.Abort(context.Background()); !errors.Is(err, ErrReservationClosed) {
		t.Fatalf("expected ErrReservationClosed, got %v", err)
	}
}

func TestReservation_CommitThenAbort(t *testing.T) {
	r := &fakeReservation{}

	if err := r.Commit(context.Background()); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	if err := r.Abort(context.Background()); !errors.Is(err, ErrReservationClosed) {
		t.Fatalf("expected ErrReservationClosed, got %v", err)
	}
}

func TestReservation_AbortThenCommit(t *testing.T) {
	r := &fakeReservation{}

	if err := r.Abort(context.Background()); err != nil {
		t.Fatalf("abort failed: %v", err)
	}

	if err := r.Commit(context.Background()); !errors.Is(err, ErrReservationClosed) {
		t.Fatalf("expected ErrReservationClosed, got %v", err)
	}
}
