package repository

import (
	"context"
	"os"
	"testing"
	"time"
	"fmt"

	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresURLRepository_CreateAndGetByID(t *testing.T) {
	databaseURL := os.Getenv(
		"DATABASE_URL",
	)

	if databaseURL == "" {
		databaseURL = "postgres://shortener:shortener@localhost:5432/shortener"
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("failed to create database pool: %v", err)
	}
	// defer pool.Close()
	t.Cleanup(func() {
		pool.Close()
	})

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("failed to ping database: %v", err)
	}

	repo := NewPostgresURLRepository(pool)

	originalURL := "https://example.com/integration-test"
	alias := fmt.Sprintf("integration-test-%d", time.Now().UnixNano())

	url := &domain.URL{
		OriginalURL: originalURL,
		CustomAlias: &alias,
		ExpiresAt:   nil,
	}

	if err := repo.Create(ctx, url); err != nil {
		t.Fatalf("failed to create URL: %v", err)
	}

	if url.ID == 0 {
		t.Fatal("expected generated ID, got 0")
	}

	if url.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be populated")
	}

	got, err := repo.GetByID(ctx, url.ID)
	if err != nil {
		t.Fatalf("failed to get URL by ID: %v", err)
	}

	if got.ID != url.ID {
		t.Fatalf("expected ID %d, got %d", url.ID, got.ID)
	}

	if got.OriginalURL != originalURL {
		t.Fatalf(
			"expected original URL %q, got %q",
			originalURL,
			got.OriginalURL,
		)
	}

	if got.CustomAlias == nil {
		t.Fatal("expected custom alias, got nil")
	}

	if *got.CustomAlias != alias {
		t.Fatalf(
			"expected alias %q, got %q",
			alias,
			*got.CustomAlias,
		)
	}

	if got.ExpiresAt != nil {
		t.Fatal("expected no expiration")
	}

	t.Cleanup(func() {
		deleteTestURL(t, pool, url.ID)
	})
}

func TestPostgresURLRepository_GetByAlias(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")

	if databaseURL == "" {
		databaseURL = "postgres://shortener:shortener@localhost:5432/shortener"
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("failed to create database pool: %v", err)
	}
	// defer pool.Close()
	t.Cleanup(func() {
		pool.Close()
	})

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("failed to ping database: %v", err)
	}

	repo := NewPostgresURLRepository(pool)

	alias := fmt.Sprintf("alias-test-%d", time.Now().UnixNano())

	url := &domain.URL{
		OriginalURL: "https://example.com/alias-test",
		CustomAlias: &alias,
	}

	if err := repo.Create(ctx, url); err != nil {
		t.Fatalf("failed to create URL: %v", err)
	}

	t.Cleanup(func() {
		deleteTestURL(t, pool, url.ID)
	})

	got, err := repo.GetByAlias(ctx, alias)
	if err != nil {
		t.Fatalf("failed to get URL by alias: %v", err)
	}

	if got.ID != url.ID {
		t.Fatalf("expected ID %d, got %d", url.ID, got.ID)
	}

	if got.OriginalURL != url.OriginalURL {
		t.Fatalf(
			"expected original URL %q, got %q",
			url.OriginalURL,
			got.OriginalURL,
		)
	}
}

func TestPostgresURLRepository_GetByIDNotFound(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")

	if databaseURL == "" {
		databaseURL = "postgres://shortener:shortener@localhost:5432/shortener"
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("failed to create database pool: %v", err)
	}
	// defer pool.Close()
	t.Cleanup(func() {
		pool.Close()
	})

	repo := NewPostgresURLRepository(pool)

	_, err = repo.GetByID(ctx, 999999999)

	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func deleteTestURL(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()

	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	_, err := pool.Exec(ctx, `DELETE FROM urls WHERE id = $1`, id)
	if err != nil {
		t.Errorf("failed to clean up test URL: %v", err)
	}
}