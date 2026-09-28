package metrics

type ServiceMetrics struct {
	metrics *Metrics
}

func NewServiceMetrics(m *Metrics) *ServiceMetrics {
	return &ServiceMetrics{
		metrics: m,
	}
}

func (m *ServiceMetrics) CacheHit() {
	m.metrics.CacheHitsTotal.Inc()
}

func (m *ServiceMetrics) CacheMiss() {
	m.metrics.CacheMissesTotal.Inc()
}

func (m *ServiceMetrics) CacheError() {
	m.metrics.CacheErrorsTotal.Inc()
}

func (m *ServiceMetrics) DBLookup() {
	m.metrics.DBLookupsTotal.Inc()
}

func (m *ServiceMetrics) Redirect() {
	m.metrics.RedirectsTotal.Inc()
}

func (m *ServiceMetrics) Creation() {
	m.metrics.CreationsTotal.Inc()
}
