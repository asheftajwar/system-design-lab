package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec
	CacheHitsTotal      prometheus.Counter
	CacheMissesTotal    prometheus.Counter
	CacheErrorsTotal    prometheus.Counter
	DBLookupsTotal      prometheus.Counter
	RedirectsTotal      prometheus.Counter
	CreationsTotal      prometheus.Counter
}

func New() *Metrics {
	m := &Metrics{
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "url_http_requests_total",
				Help: "Total number of HTTP requests.",
			},
			[]string{"method", "path", "status"},
		),

		HTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "url_http_request_duration_seconds",
				Help:    "HTTP request duration in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "path"},
		),

		CacheHitsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "url_cache_hits_total",
				Help: "Total number of cache hits.",
			},
		),

		CacheMissesTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "url_cache_misses_total",
				Help: "Total number of cache misses.",
			},
		),

		CacheErrorsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "url_cache_errors_total",
				Help: "Total number of cache errors.",
			},
		),

		DBLookupsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "url_db_lookups_total",
				Help: "Total number of database lookups.",
			},
		),

		RedirectsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "url_redirects_total",
				Help: "Total number of successful redirects.",
			},
		),

		CreationsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "url_creations_total",
				Help: "Total number of URL creations.",
			},
		),
	}

	return m
}

func (m *Metrics) Register(registry *prometheus.Registry) error {
	collectors := []prometheus.Collector{
		m.HTTPRequestsTotal,
		m.HTTPRequestDuration,
		m.CacheHitsTotal,
		m.CacheMissesTotal,
		m.CacheErrorsTotal,
		m.DBLookupsTotal,
		m.RedirectsTotal,
		m.CreationsTotal,
	}

	for _, collector := range collectors {
		if err := registry.Register(collector); err != nil {
			return err
		}
	}

	return nil
}

func (m *Metrics) ObserveHTTPRequest(
	method string,
	path string,
	status int,
	duration time.Duration,
) {
	m.HTTPRequestsTotal.WithLabelValues(
		method,
		path,
		strconv.Itoa(status),
	).Inc()

	m.HTTPRequestDuration.WithLabelValues(
		method,
		path,
	).Observe(duration.Seconds())
}
