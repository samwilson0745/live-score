// Package metrics defines the Prometheus instruments used across the app.
// Labels are deliberately low-cardinality (route patterns, not raw paths).
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

// Metrics is nil-safe: every method is a no-op on a nil receiver.
type Metrics struct {
	registry *prometheus.Registry

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
	cacheEvents  *prometheus.CounterVec
	provReqs     *prometheus.CounterVec
	provDuration *prometheus.HistogramVec
	ingestRuns   *prometheus.CounterVec
	ingestLast   *prometheus.GaugeVec
	ingestDur    *prometheus.HistogramVec
	wsConns      prometheus.Gauge
	shed         prometheus.Counter
}

func New() *Metrics {
	r := prometheus.NewRegistry()
	m := &Metrics{
		registry: r,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total", Help: "HTTP requests by route, method and status."},
			[]string{"route", "method", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "http_request_duration_seconds", Help: "HTTP request latency.",
			Buckets: prometheus.DefBuckets}, []string{"route"}),
		cacheEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cache_events_total", Help: "Cache results: hit, miss, bypass, get_error, set_error."},
			[]string{"result"}),
		provReqs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "provider_requests_total", Help: "Provider calls by outcome."},
			[]string{"provider", "outcome"}),
		provDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "provider_request_duration_seconds", Help: "Provider call latency.",
			Buckets: prometheus.DefBuckets}, []string{"provider"}),
		ingestRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ingest_runs_total", Help: "Ingestion runs by provider and outcome."},
			[]string{"provider", "outcome"}),
		ingestLast: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ingest_last_success_timestamp_seconds", Help: "Unix time of the last successful ingestion."},
			[]string{"provider", "sport"}),
		ingestDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "ingest_duration_seconds", Help: "Ingestion run duration.",
			Buckets: prometheus.DefBuckets}, []string{"provider"}),
		wsConns: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "websocket_connections", Help: "Open WebSocket connections."}),
		shed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "provider_load_shed_total", Help: "Requests rejected because the provider concurrency limit was reached."}),
	}
	r.MustRegister(m.httpRequests, m.httpDuration, m.cacheEvents, m.provReqs, m.provDuration,
		m.ingestRuns, m.ingestLast, m.ingestDur, m.wsConns, m.shed,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) ObserveHTTP(route, method, status string, d time.Duration) {
	if m == nil {
		return
	}
	m.httpRequests.WithLabelValues(route, method, status).Inc()
	m.httpDuration.WithLabelValues(route).Observe(d.Seconds())
}

func (m *Metrics) CacheEvent(result string) {
	if m != nil {
		m.cacheEvents.WithLabelValues(result).Inc()
	}
}

func (m *Metrics) ProviderCall(provider, outcome string, d time.Duration) {
	if m == nil {
		return
	}
	m.provReqs.WithLabelValues(provider, outcome).Inc()
	m.provDuration.WithLabelValues(provider).Observe(d.Seconds())
}

func (m *Metrics) LoadShed() {
	if m != nil {
		m.shed.Inc()
	}
}

func (m *Metrics) IngestRun(provider, sport, outcome string, d time.Duration, at time.Time) {
	if m == nil {
		return
	}
	m.ingestRuns.WithLabelValues(provider, outcome).Inc()
	m.ingestDur.WithLabelValues(provider).Observe(d.Seconds())
	if outcome == "success" {
		m.ingestLast.WithLabelValues(provider, sport).Set(float64(at.Unix()))
	}
}

func (m *Metrics) WSConnections(delta float64) {
	if m != nil {
		m.wsConns.Add(delta)
	}
}
