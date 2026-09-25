package repository

import (
	"context"
	"errors"

	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound       = errors.New("url not found")
	ErrDuplicateAlias = errors.New("custom alias already exists")
)

type PostgresURLRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresURLRepository(pool *pgxpool.Pool) *PostgresURLRepository {
	return &PostgresURLRepository{
		pool: pool,
	}
}

func (r *PostgresURLRepository) Create(
	ctx context.Context,
	url *domain.URL,
) error {
	query := `
		INSERT INTO urls (
			original_url,
			custom_alias,
			expires_at
		)
		VALUES ($1, $2, $3)
		RETURNING id, created_at
	`

	err := r.pool.QueryRow(
		ctx,
		query,
		url.OriginalURL,
		url.CustomAlias,
		url.ExpiresAt,
	).Scan(
		&url.ID,
		&url.CreatedAt,
	)

	if err != nil {
		var pgErr *pgconn.PgError

		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrDuplicateAlias
		}

		return err
	}

	return nil
}

func (r *PostgresURLRepository) GetByID(
	ctx context.Context,
	id int64,
) (*domain.URL, error) {
	query := `
		SELECT
			id,
			original_url,
			custom_alias,
			expires_at,
			created_at
		FROM urls
		WHERE id = $1
	`

	var url domain.URL

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&url.ID,
		&url.OriginalURL,
		&url.CustomAlias,
		&url.ExpiresAt,
		&url.CreatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}

	if err != nil {
		return nil, err
	}

	return &url, nil
}

func (r *PostgresURLRepository) GetByAlias(
	ctx context.Context,
	alias string,
) (*domain.URL, error) {
	query := `
		SELECT
			id,
			original_url,
			custom_alias,
			expires_at,
			created_at
		FROM urls
		WHERE custom_alias = $1
	`

	var url domain.URL

	err := r.pool.QueryRow(ctx, query, alias).Scan(
		&url.ID,
		&url.OriginalURL,
		&url.CustomAlias,
		&url.ExpiresAt,
		&url.CreatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}

	if err != nil {
		return nil, err
	}

	return &url, nil
}