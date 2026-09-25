package cache

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("cache key not found")

type Cache interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string) error
	Delete(ctx context.Context, key string) error
}
