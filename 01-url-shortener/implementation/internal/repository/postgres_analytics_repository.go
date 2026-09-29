package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresAnalyticsRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresAnalyticsRepository(pool *pgxpool.Pool) *PostgresAnalyticsRepository {
	return &PostgresAnalyticsRepository{
		pool: pool,
	}
}

func (r *PostgresAnalyticsRepository) RecordRedirect(
	ctx context.Context,
	urlID int64,
	accessedAt time.Time,
) error {
	const query = `
		INSERT INTO url_analytics (
			url_id,
			redirect_count,
			last_accessed_at
		)
		VALUES ($1, 1, $2)
		ON CONFLICT (url_id)
		DO UPDATE SET
			redirect_count = url_analytics.redirect_count + 1,
			last_accessed_at = EXCLUDED.last_accessed_at
	`

	_, err := r.pool.Exec(
		ctx,
		query,
		urlID,
		accessedAt,
	)

	return err
}

func (r *PostgresAnalyticsRepository) GetAnalytics(
	ctx context.Context,
	urlID int64,
) (*URLAnalytics, error) {
	const query = `
		SELECT
			url_id,
			redirect_count,
			last_accessed_at
		FROM url_analytics
		WHERE url_id = $1
	`

	var analytics URLAnalytics

	err := r.pool.QueryRow(
		ctx,
		query,
		urlID,
	).Scan(
		&analytics.URLID,
		&analytics.RedirectCount,
		&analytics.LastAccessedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}

	if err != nil {
		return nil, err
	}

	return &analytics, nil
}
