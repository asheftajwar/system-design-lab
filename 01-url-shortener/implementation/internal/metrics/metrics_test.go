package metrics

import (
	"github.com/prometheus/client_golang/prometheus/testutil"
	"testing"
)

func TestMetricsAnalyticsEventDropped(t *testing.T) {
	m := New()

	m.AnalyticsEventDropped()

	if got := testutil.ToFloat64(m.AnalyticsEventsDroppedTotal); got != 1 {
		t.Fatalf("expected 1 dropped analytics event, got %v", got)
	}
}
