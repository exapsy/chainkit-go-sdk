// Package prometheus provides a Prometheus metrics recorder for the chainkit scoring engine.
//
// Usage:
//
//	import (
//	    "github.com/exapsy/chainkit/scoring"
//	    promrecorder "github.com/exapsy/chainkit/scoring/metrics/prometheus"
//	)
//
//	engine := scoring.NewEngine(
//	    scoring.WithMetrics(promrecorder.NewRecorder(promrecorder.DefaultConfig())),
//	)
package prometheus

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/exapsy/chainkit/scoring/metrics"
)

// Config holds Prometheus metrics configuration.
type Config struct {
	// Namespace for all metrics. Default: "chainkit"
	Namespace string

	// Subsystem for all metrics. Default: "scoring"
	Subsystem string

	// Registry to register metrics with. Default: prometheus.DefaultRegisterer
	Registry prometheus.Registerer

	// LatencyBuckets for histogram metrics.
	// Default: exponential buckets covering 1ms to 10s.
	LatencyBuckets []float64
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Namespace:      "chainkit",
		Subsystem:      "scoring",
		Registry:       prometheus.DefaultRegisterer,
		LatencyBuckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}
}

// Recorder implements metrics.Recorder using Prometheus.
// All methods are safe for concurrent use.
type Recorder struct {
	scoreGauge          *prometheus.GaugeVec
	effectiveScoreGauge *prometheus.GaugeVec
	eventsCounter       *prometheus.CounterVec
	latencyHistogram    *prometheus.HistogramVec
	providerRankGauge   *prometheus.GaugeVec
	providersGauge      prometheus.Gauge
	storeOpsCounter     *prometheus.CounterVec
	storeLatencyHist    *prometheus.HistogramVec
	cacheHitsCounter    *prometheus.CounterVec
}

// NewRecorder creates a new Prometheus-based metrics recorder.
// Metrics are registered with the provided registry (or prometheus.DefaultRegisterer if nil).
func NewRecorder(config Config) *Recorder {
	if config.Namespace == "" {
		config.Namespace = "chainkit"
	}
	if config.Subsystem == "" {
		config.Subsystem = "scoring"
	}
	if config.Registry == nil {
		config.Registry = prometheus.DefaultRegisterer
	}
	if len(config.LatencyBuckets) == 0 {
		config.LatencyBuckets = DefaultConfig().LatencyBuckets
	}

	factory := promauto.With(config.Registry)

	return &Recorder{
		scoreGauge: factory.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: config.Namespace,
			Subsystem: config.Subsystem,
			Name:      "score",
			Help:      "Current score value by component type (base, penalties, effective).",
		}, []string{metrics.Labels.Provider, metrics.Labels.ScoreType}),

		effectiveScoreGauge: factory.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: config.Namespace,
			Subsystem: config.Subsystem,
			Name:      "effective_score",
			Help:      "Current effective score per provider after all penalties.",
		}, []string{metrics.Labels.Provider}),

		eventsCounter: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: config.Namespace,
			Subsystem: config.Subsystem,
			Name:      "events_total",
			Help:      "Total scoring events recorded, by provider, event type, and outcome.",
		}, []string{metrics.Labels.Provider, metrics.Labels.EventType, metrics.Labels.Success}),

		latencyHistogram: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: config.Namespace,
			Subsystem: config.Subsystem,
			Name:      "latency_seconds",
			Help:      "Provider operation latency in seconds.",
			Buckets:   config.LatencyBuckets,
		}, []string{metrics.Labels.Provider, metrics.Labels.Operation}),

		providerRankGauge: factory.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: config.Namespace,
			Subsystem: config.Subsystem,
			Name:      "provider_rank",
			Help:      "Current provider rank by effective score (1 = best).",
		}, []string{metrics.Labels.Provider}),

		providersGauge: factory.NewGauge(prometheus.GaugeOpts{
			Namespace: config.Namespace,
			Subsystem: config.Subsystem,
			Name:      "providers_total",
			Help:      "Total number of registered providers.",
		}),

		storeOpsCounter: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: config.Namespace,
			Subsystem: config.Subsystem,
			Name:      "store_operations_total",
			Help:      "Total store operations, by store, operation, and outcome.",
		}, []string{metrics.Labels.Store, metrics.Labels.Operation, metrics.Labels.Success}),

		storeLatencyHist: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: config.Namespace,
			Subsystem: config.Subsystem,
			Name:      "store_latency_seconds",
			Help:      "Store operation latency in seconds.",
			Buckets:   config.LatencyBuckets,
		}, []string{metrics.Labels.Store, metrics.Labels.Operation}),

		cacheHitsCounter: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: config.Namespace,
			Subsystem: config.Subsystem,
			Name:      "cache_hits_total",
			Help:      "Total cache lookups, labelled by store and whether it was a hit.",
		}, []string{metrics.Labels.Store, metrics.Labels.CacheHit}),
	}
}

// RecordScoreChange records a change in a provider's score component.
func (r *Recorder) RecordScoreChange(_ context.Context, provider string, scoreType metrics.ScoreType, _, newValue float64) {
	r.scoreGauge.WithLabelValues(provider, string(scoreType)).Set(newValue)
}

// RecordEffectiveScore records the final computed score for a provider.
func (r *Recorder) RecordEffectiveScore(_ context.Context, provider string, score float64) {
	r.effectiveScoreGauge.WithLabelValues(provider).Set(score)
}

// RecordEvent records a scoring event (health check, operation success/failure, etc.).
func (r *Recorder) RecordEvent(_ context.Context, provider string, eventType string, success bool) {
	successStr := boolLabel(success)
	r.eventsCounter.WithLabelValues(provider, eventType, successStr).Inc()
}

// RecordLatency records the latency of a provider operation.
func (r *Recorder) RecordLatency(_ context.Context, provider string, operation string, duration time.Duration) {
	r.latencyHistogram.WithLabelValues(provider, operation).Observe(duration.Seconds())
}

// RecordStoreOperation records a storage backend operation.
func (r *Recorder) RecordStoreOperation(_ context.Context, store string, operation string, duration time.Duration, err error) {
	successStr := boolLabel(err == nil)
	r.storeOpsCounter.WithLabelValues(store, operation, successStr).Inc()
	r.storeLatencyHist.WithLabelValues(store, operation).Observe(duration.Seconds())
}

// RecordCacheHit records a cache hit or miss event.
func (r *Recorder) RecordCacheHit(_ context.Context, store string, hit bool) {
	r.cacheHitsCounter.WithLabelValues(store, boolLabel(hit)).Inc()
}

// RecordProviderRank records the current rank of a provider.
func (r *Recorder) RecordProviderRank(_ context.Context, provider string, rank int, totalProviders int) {
	r.providerRankGauge.WithLabelValues(provider).Set(float64(rank))
	r.providersGauge.Set(float64(totalProviders))
}

// Ensure Recorder implements metrics.Recorder at compile time.
var _ metrics.Recorder = (*Recorder)(nil)

func boolLabel(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
