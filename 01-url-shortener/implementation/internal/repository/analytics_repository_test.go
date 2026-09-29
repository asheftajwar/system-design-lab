package repository

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresAnalyticsRepository_RecordRedirect(t *testing.T) {
	repo, pool := newTestAnalyticsRepository(t)
	ctx := context.Background()

	url := &domain.URL{
		OriginalURL: fmt.Sprintf(
			"https://analytics.example/%d",
			time.Now().UnixNano(),
		),
	}

	if err := createTestURL(t, pool, url); err != nil {
		t.Fatalf("failed to create test URL: %v", err)
	}

	t.Cleanup(func() {
		deleteTestURL(t, pool, url.ID)
	})

	accessedAt := time.Date(
		2026,
		time.January,
		2,
		10,
		30,
		0,
		0,
		time.UTC,
	)

	if err := repo.RecordRedirect(
		ctx,
		url.ID,
		accessedAt,
	); err != nil {
		t.Fatalf("failed to record redirect: %v", err)
	}

	analytics, err := repo.GetAnalytics(ctx, url.ID)
	if err != nil {
		t.Fatalf("failed to get analytics: %v", err)
	}

	if analytics.URLID != url.ID {
		t.Fatalf(
			"expected URL ID %d, got %d",
			url.ID,
			analytics.URLID,
		)
	}

	if analytics.RedirectCount != 1 {
		t.Fatalf(
			"expected redirect count 1, got %d",
			analytics.RedirectCount,
		)
	}

	if analytics.LastAccessedAt == nil {
		t.Fatal("expected last accessed time, got nil")
	}

	if !analytics.LastAccessedAt.Equal(accessedAt) {
		t.Fatalf(
			"expected last accessed time %v, got %v",
			accessedAt,
			*analytics.LastAccessedAt,
		)
	}
}

func TestPostgresAnalyticsRepository_IncrementsRedirectCount(
	t *testing.T,
) {
	repo, pool := newTestAnalyticsRepository(t)
	ctx := context.Background()

	url := &domain.URL{
		OriginalURL: fmt.Sprintf(
			"https://analytics-increment.example/%d",
			time.Now().UnixNano(),
		),
	}

	if err := createTestURL(t, pool, url); err != nil {
		t.Fatalf("failed to create test URL: %v", err)
	}

	t.Cleanup(func() {
		deleteTestURL(t, pool, url.ID)
	})

	firstAccess := time.Date(
		2026,
		time.January,
		2,
		10,
		30,
		0,
		0,
		time.UTC,
	)

	secondAccess := firstAccess.Add(time.Minute)
	thirdAccess := secondAccess.Add(time.Minute)

	times := []time.Time{
		firstAccess,
		secondAccess,
		thirdAccess,
	}

	for _, accessedAt := range times {
		if err := repo.RecordRedirect(
			ctx,
			url.ID,
			accessedAt,
		); err != nil {
			t.Fatalf(
				"failed to record redirect: %v",
				err,
			)
		}
	}

	analytics, err := repo.GetAnalytics(ctx, url.ID)
	if err != nil {
		t.Fatalf("failed to get analytics: %v", err)
	}

	if analytics.RedirectCount != 3 {
		t.Fatalf(
			"expected redirect count 3, got %d",
			analytics.RedirectCount,
		)
	}

	if analytics.LastAccessedAt == nil {
		t.Fatal("expected last accessed time, got nil")
	}

	if !analytics.LastAccessedAt.Equal(thirdAccess) {
		t.Fatalf(
			"expected last accessed time %v, got %v",
			thirdAccess,
			*analytics.LastAccessedAt,
		)
	}
}

func TestPostgresAnalyticsRepository_GetAnalyticsNotFound(
	t *testing.T,
) {
	repo, pool := newTestAnalyticsRepository(t)
	ctx := context.Background()

	_, err := repo.GetAnalytics(ctx, 999999999)

	if err != ErrNotFound {
		t.Fatalf(
			"expected ErrNotFound, got %v",
			err,
		)
	}

	_ = pool
}

func TestPostgresAnalyticsRepository_KeepsURLsIndependent(
	t *testing.T,
) {
	repo, pool := newTestAnalyticsRepository(t)
	ctx := context.Background()

	url1 := &domain.URL{
		OriginalURL: fmt.Sprintf(
			"https://analytics-one.example/%d",
			time.Now().UnixNano(),
		),
	}

	url2 := &domain.URL{
		OriginalURL: fmt.Sprintf(
			"https://analytics-two.example/%d",
			time.Now().UnixNano(),
		),
	}

	if err := createTestURL(t, pool, url1); err != nil {
		t.Fatalf(
			"failed to create first URL: %v",
			err,
		)
	}

	if err := createTestURL(t, pool, url2); err != nil {
		t.Fatalf(
			"failed to create second URL: %v",
			err,
		)
	}

	t.Cleanup(func() {
		deleteTestURL(t, pool, url1.ID)
		deleteTestURL(t, pool, url2.ID)
	})

	accessedAt := time.Date(
		2026,
		time.January,
		2,
		10,
		30,
		0,
		0,
		time.UTC,
	)

	for i := 0; i < 3; i++ {
		if err := repo.RecordRedirect(
			ctx,
			url1.ID,
			accessedAt.Add(
				time.Duration(i)*time.Minute,
			),
		); err != nil {
			t.Fatalf(
				"failed to record redirect for URL 1: %v",
				err,
			)
		}
	}

	for i := 0; i < 2; i++ {
		if err := repo.RecordRedirect(
			ctx,
			url2.ID,
			accessedAt.Add(
				time.Duration(i)*time.Minute,
			),
		); err != nil {
			t.Fatalf(
				"failed to record redirect for URL 2: %v",
				err,
			)
		}
	}

	analytics1, err := repo.GetAnalytics(ctx, url1.ID)
	if err != nil {
		t.Fatalf(
			"failed to get URL 1 analytics: %v",
			err,
		)
	}

	analytics2, err := repo.GetAnalytics(ctx, url2.ID)
	if err != nil {
		t.Fatalf(
			"failed to get URL 2 analytics: %v",
			err,
		)
	}

	if analytics1.RedirectCount != 3 {
		t.Fatalf(
			"expected URL 1 count 3, got %d",
			analytics1.RedirectCount,
		)
	}

	if analytics2.RedirectCount != 2 {
		t.Fatalf(
			"expected URL 2 count 2, got %d",
			analytics2.RedirectCount,
		)
	}
}

func TestPostgresAnalyticsRepository_ConcurrentRedirects(
	t *testing.T,
) {
	repo, pool := newTestAnalyticsRepository(t)
	ctx := context.Background()

	url := &domain.URL{
		OriginalURL: fmt.Sprintf(
			"https://analytics-concurrent.example/%d",
			time.Now().UnixNano(),
		),
	}

	if err := createTestURL(t, pool, url); err != nil {
		t.Fatalf(
			"failed to create test URL: %v",
			err,
		)
	}

	t.Cleanup(func() {
		deleteTestURL(t, pool, url.ID)
	})

	const redirectCount = 20

	accessedAt := time.Date(
		2026,
		time.January,
		2,
		10,
		30,
		0,
		0,
		time.UTC,
	)

	var wg sync.WaitGroup
	errCh := make(chan error, redirectCount)

	for i := 0; i < redirectCount; i++ {
		wg.Add(1)

		go func(offset int) {
			defer wg.Done()

			err := repo.RecordRedirect(
				ctx,
				url.ID,
				accessedAt.Add(
					time.Duration(offset)*time.Millisecond,
				),
			)

			errCh <- err
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf(
				"concurrent redirect recording failed: %v",
				err,
			)
		}
	}

	analytics, err := repo.GetAnalytics(ctx, url.ID)
	if err != nil {
		t.Fatalf(
			"failed to get analytics: %v",
			err,
		)
	}

	if analytics.RedirectCount != redirectCount {
		t.Fatalf(
			"expected redirect count %d, got %d",
			redirectCount,
			analytics.RedirectCount,
		)
	}

	if analytics.LastAccessedAt == nil {
		t.Fatal("expected last accessed time, got nil")
	}
}

func newTestAnalyticsRepository(
	t *testing.T,
) (*PostgresAnalyticsRepository, *pgxpool.Pool) {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")

	if databaseURL == "" {
		databaseURL = "postgres://shortener:shortener@localhost:5432/shortener"
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf(
			"failed to create database pool: %v",
			err,
		)
	}

	t.Cleanup(func() {
		pool.Close()
	})

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf(
			"failed to ping database: %v",
			err,
		)
	}

	return NewPostgresAnalyticsRepository(pool), pool
}

func createTestURL(
	t *testing.T,
	pool *pgxpool.Pool,
	url *domain.URL,
) error {
	t.Helper()

	const query = `
		INSERT INTO urls (
			original_url
		)
		VALUES ($1)
		RETURNING id, created_at
	`

	return pool.QueryRow(
		context.Background(),
		query,
		url.OriginalURL,
	).Scan(
		&url.ID,
		&url.CreatedAt,
	)
}
