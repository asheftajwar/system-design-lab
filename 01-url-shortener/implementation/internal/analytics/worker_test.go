package analytics

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/repository"
)

type fakeAnalyticsRepository struct {
	mu sync.Mutex

	events []RedirectEvent

	err error
}

func (f *fakeAnalyticsRepository) RecordRedirect(
	_ context.Context,
	urlID int64,
	accessedAt time.Time,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return f.err
	}

	f.events = append(f.events, RedirectEvent{
		URLID:      urlID,
		AccessedAt: accessedAt,
	})

	return nil
}

func (f *fakeAnalyticsRepository) GetAnalytics(
	_ context.Context,
	_ int64,
) (*repository.URLAnalytics, error) {
	return nil, repository.ErrNotFound
}

func (f *fakeAnalyticsRepository) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.err = err
}

func (f *fakeAnalyticsRepository) EventCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.events)
}

func TestWorkerFlushesEvents(t *testing.T) {
	repo := &fakeAnalyticsRepository{}

	worker := NewWorker(
		repo,
		WorkerConfig{
			BufferSize:  10,
			FlushSize:   2,
			FlushPeriod: time.Hour,
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- worker.Run(ctx)
	}()

	accessedAt := time.Now().UTC()

	if !worker.Emit(RedirectEvent{
		URLID:      101,
		AccessedAt: accessedAt,
	}) {
		t.Fatal("expected first event to be accepted")
	}

	if !worker.Emit(RedirectEvent{
		URLID:      102,
		AccessedAt: accessedAt,
	}) {
		t.Fatal("expected second event to be accepted")
	}

	waitForEventCount(t, repo, 2)

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("worker returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestWorkerFlushesOnShutdown(t *testing.T) {
	repo := &fakeAnalyticsRepository{}

	worker := NewWorker(
		repo,
		WorkerConfig{
			BufferSize:  10,
			FlushSize:   100,
			FlushPeriod: time.Hour,
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- worker.Run(ctx)
	}()

	if !worker.Emit(RedirectEvent{
		URLID:      200,
		AccessedAt: time.Now().UTC(),
	}) {
		t.Fatal("expected event to be accepted")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("worker returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}

	if got := repo.EventCount(); got != 1 {
		t.Fatalf("expected 1 persisted event, got %d", got)
	}
}

func TestWorkerDropsEventsWhenBufferIsFull(t *testing.T) {
	repo := &fakeAnalyticsRepository{}

	worker := NewWorker(
		repo,
		WorkerConfig{
			BufferSize: 1,
			FlushSize:  100,
		},
	)

	first := worker.Emit(RedirectEvent{
		URLID:      1,
		AccessedAt: time.Now().UTC(),
	})

	second := worker.Emit(RedirectEvent{
		URLID:      2,
		AccessedAt: time.Now().UTC(),
	})

	if !first {
		t.Fatal("expected first event to be accepted")
	}

	if second {
		t.Fatal("expected second event to be dropped when buffer is full")
	}
}

func TestWorkerRecoversAfterRepositoryError(t *testing.T) {
	repo := &fakeAnalyticsRepository{
		err: errors.New("database unavailable"),
	}

	worker := NewWorker(
		repo,
		WorkerConfig{
			BufferSize:  10,
			FlushSize:   1,
			FlushPeriod: 20 * time.Millisecond,
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- worker.Run(ctx)
	}()

	if !worker.Emit(RedirectEvent{
		URLID:      300,
		AccessedAt: time.Now().UTC(),
	}) {
		t.Fatal("expected event to be accepted")
	}

	time.Sleep(50 * time.Millisecond)

	if got := repo.EventCount(); got != 0 {
		t.Fatalf("expected failed event not to be persisted, got %d", got)
	}

	repo.SetError(nil)

	if !worker.Emit(RedirectEvent{
		URLID:      301,
		AccessedAt: time.Now().UTC(),
	}) {
		t.Fatal("expected recovery event to be accepted")
	}

	waitForEventCount(t, repo, 2)

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("worker returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func waitForEventCount(
	t *testing.T,
	repo *fakeAnalyticsRepository,
	expected int,
) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)

	for time.Now().Before(deadline) {
		if repo.EventCount() >= expected {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf(
		"timed out waiting for %d events, got %d",
		expected,
		repo.EventCount(),
	)
}