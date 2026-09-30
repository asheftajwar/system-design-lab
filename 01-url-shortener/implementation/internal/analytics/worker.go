package analytics

import (
	"context"
	"log"
	"time"

	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/repository"
)

type Worker struct {
	repository     repository.AnalyticsRepository
	events         chan RedirectEvent
	flushSize      int
	flushPeriod    time.Duration
	onEventDropped func()
}

type WorkerConfig struct {
	BufferSize     int
	FlushSize      int
	FlushPeriod    time.Duration
	OnEventDropped func()
}

func NewWorker(
	repo repository.AnalyticsRepository,
	cfg WorkerConfig,
) *Worker {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 1000
	}

	if cfg.FlushSize <= 0 {
		cfg.FlushSize = 100
	}

	if cfg.FlushPeriod <= 0 {
		cfg.FlushPeriod = time.Second
	}

	return &Worker{
		repository:     repo,
		events:         make(chan RedirectEvent, cfg.BufferSize),
		flushSize:      cfg.FlushSize,
		flushPeriod:    cfg.FlushPeriod,
		onEventDropped: cfg.OnEventDropped,
	}
}

// Emit queues an analytics event without blocking the redirect path.
//
// It returns false when the buffer is full. V1 deliberately drops the
// analytics event rather than making redirects wait for analytics storage.
func (w *Worker) Emit(event RedirectEvent) bool {
	select {
	case w.events <- event:
		return true
	default:
		if w.onEventDropped != nil {
			w.onEventDropped()
		}

		return false
	}
}

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.flushPeriod)
	defer ticker.Stop()

	batch := make([]RedirectEvent, 0, w.flushSize)

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}

		for _, event := range batch {
			if err := w.repository.RecordRedirect(
				ctx,
				event.URLID,
				event.AccessedAt,
			); err != nil {
				return err
			}
		}

		batch = batch[:0]
		return nil
	}

	drain := func() {
		for {
			select {
			case event := <-w.events:
				batch = append(batch, event)
			default:
				return
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			// Drain events that were already accepted before shutdown.
			drain()

			// The worker's parent context is canceled during shutdown.
			// Use a separate bounded context so the final analytics flush
			// can still reach PostgreSQL.
			shutdownCtx, cancel := context.WithTimeout(
				context.Background(),
				5*time.Second,
			)
			defer cancel()

			if len(batch) == 0 {
				return nil
			}

			for _, event := range batch {
				if err := w.repository.RecordRedirect(
					shutdownCtx,
					event.URLID,
					event.AccessedAt,
				); err != nil {
					return err
				}
			}

			return nil

		case event := <-w.events:
			batch = append(batch, event)

			if len(batch) >= w.flushSize {
				if err := flush(); err != nil {
					log.Printf("analytics worker flush failed: %v", err)
				}
			}

		case <-ticker.C:
			if err := flush(); err != nil {
				log.Printf("analytics worker flush failed: %v", err)
			}
		}
	}
}
