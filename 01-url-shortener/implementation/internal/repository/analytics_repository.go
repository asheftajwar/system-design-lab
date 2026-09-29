package repository

import (
	"context"
	"time"
)

type URLAnalytics struct {
	URLID          int64
	RedirectCount  int64
	LastAccessedAt *time.Time
}

type AnalyticsRepository interface {
	RecordRedirect(
		ctx context.Context,
		urlID int64,
		accessedAt time.Time,
	) error

	GetAnalytics(
		ctx context.Context,
		urlID int64,
	) (*URLAnalytics, error)
}
