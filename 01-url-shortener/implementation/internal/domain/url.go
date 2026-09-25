package domain

import "time"

type URL struct {
	ID          int64
	OriginalURL string
	CustomAlias *string
	ExpiresAt   *time.Time
	CreatedAt   time.Time
}