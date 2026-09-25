package repository

import (
	"context"

	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/domain"
)

type URLRepository interface {
	Create(ctx context.Context, url *domain.URL) error
	GetByID(ctx context.Context, id int64) (*domain.URL, error)
	GetByAlias(ctx context.Context, alias string) (*domain.URL, error)
}