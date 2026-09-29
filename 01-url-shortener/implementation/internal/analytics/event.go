package analytics

import "time"

type RedirectEvent struct {
	URLID      int64
	AccessedAt time.Time
}