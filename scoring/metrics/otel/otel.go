// Package otel provides an OpenTelemetry metrics recorder for the chainkit scoring engine.
//
// Usage:
//
//	import (
//	    "github.com/exapsy/chainkit/scoring"
//	    otelrecorder "github.com/exapsy/chainkit/scoring/metrics/otel"
//	)
//
//	recorder, err := otelrecorder.NewRecorder(otelrecorder.DefaultConfig())
//	if err != nil {
//	    log.Fatal(err)
//	}
//	engine := scoring.NewEngine(
//	    scoring.WithMetrics(recorder),
//	)
package otel

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	scoringmetrics "github.com/exapsy/chainkit/scoring/metrics"
)

// Config holds OpenTelemetry metrics configuration.
type Config struct {
	// MeterName is the name of the meter. Default: "chainkit.scoring"
	MeterName string

	// MeterProvider to use. Default: otel.GetMeterProvider()
	MeterProvider metric.MeterProvider
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		MeterName:     "chainkit.scoring",
		MeterProvider: nil, // uses global provider
	}
}

// Recorder implements scoringmetrics.Recorder using OpenTelemetry.
// All methods are safe for concurrent use.
type Recorder struct {
	eventsCounter    metric.Int64Counter
	latencyHistogram metric.Float64Histogram
	storeOpsCounter  metric.Int64Counter
	storeLatencyHist metric.Float64Histogram
	cacheHitsCounter metric.Int64Counter

	// Observable gauge state — protected by mu.
	mu              sync.Mutex
	scores          map[string]map[scoringmetrics.ScoreType]float64
	effectiveScores map[string]float64
	providerRanks   map[string]int
	totalProviders  int
}

// NewRecorder creates a new OpenTelemetry-based metrics recorder.
// Returns an error if any instrument cannot be created.
func NewRecorder(config Config) (*Recorder, error) {
	if config.MeterName == "" {
		config.MeterName = "chainkit.scoring"
	}

	provider := config.MeterProvider
	if provider == nil {
		provider = otel.GetMeterProvider()
	}

	meter := provider.Meter(config.MeterName)

	r := &Recorder{
		scores:          make(map[string]map[scoringmetrics.ScoreType]float64),
		effectiveScores: make(map[string]float64),
		providerRanks:   make(map[string]int),
	}

	var err error

	// Observable gauges for score values — the SDK calls the callback at collection time.
	if _, err = meter.Float64ObservableGauge(
		"chainkit.scoring.score",
		metric.WithDescription("Current score value by component type."),
		metric.WithFloat64Callback(r.observeScores),
	); err != nil {
		return nil, err
	}

	if _, err = meter.Float64ObservableGauge(
		"chainkit.scoring.effective_score",
		metric.WithDescription("Current effective score per provider after all penalties."),
		metric.WithFloat64Callback(r.observeEffectiveScores),
	); err != nil {
		return nil, err
	}

	if _, err = meter.Int64ObservableGauge(
		"chainkit.scoring.provider_rank",
		metric.WithDescription("Current provider rank by effective score (1 = best)."),
		metric.WithInt64Callback(r.observeProviderRanks),
	); err != nil {
		return nil, err
	}

	if _, err = meter.Int64ObservableGauge(
		"chainkit.scoring.providers_total",
		metric.WithDescription("Total number of registered providers."),
		metric.WithInt64Callback(r.observeTotalProviders),
	); err != nil {
		return nil, err
	}

	// Counters and histograms for direct recording.
	if r.eventsCounter, err = meter.Int64Counter(
		"chainkit.scoring.events_total",
		metric.WithDescription("Total scoring events, by provider, event type, and outcome."),
	); err != nil {
		return nil, err
	}

	if r.latencyHistogram, err = meter.Float64Histogram(
		"chainkit.scoring.latency_seconds",
		metric.WithDescription("Provider operation latency in seconds."),
		metric.WithUnit("s"),
	); err != nil {
		return nil, err
	}

	if r.storeOpsCounter, err = meter.Int64Counter(
		"chainkit.scoring.store_operations_total",
		metric.WithDescription("Total store operations, by store, operation, and outcome."),
	); err != nil {
		return nil, err
	}

	if r.storeLatencyHist, err = meter.Float64Histogram(
		"chainkit.scoring.store_latency_seconds",
		metric.WithDescription("Store operation latency in seconds."),
		metric.WithUnit("s"),
	); err != nil {
		return nil, err
	}

	if r.cacheHitsCounter, err = meter.Int64Counter(
		"chainkit.scoring.cache_hits_total",
		metric.WithDescription("Total cache lookups, by store and whether it was a hit."),
	); err != nil {
		return nil, err
	}

	return r, nil
}

// observeScores is called by the OTel SDK to collect current score values.
func (r *Recorder) observeScores(_ context.Context, observer metric.Float64Observer) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for provider, scoresByType := range r.scores {
		for scoreType, value := range scoresByType {
			observer.Observe(value,
				metric.WithAttributes(
					attribute.String(scoringmetrics.Labels.Provider, provider),
					attribute.String(scoringmetrics.Labels.ScoreType, string(scoreType)),
				),
			)
		}
	}
	return nil
}

// observeEffectiveScores is called by the OTel SDK to collect effective scores.
func (r *Recorder) observeEffectiveScores(_ context.Context, observer metric.Float64Observer) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for provider, score := range r.effectiveScores {
		observer.Observe(score,
			metric.WithAttributes(attribute.String(scoringmetrics.Labels.Provider, provider)),
		)
	}
	return nil
}

// observeProviderRanks is called by the OTel SDK to collect provider ranks.
func (r *Recorder) observeProviderRanks(_ context.Context, observer metric.Int64Observer) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for provider, rank := range r.providerRanks {
		observer.Observe(int64(rank),
			metric.WithAttributes(attribute.String(scoringmetrics.Labels.Provider, provider)),
		)
	}
	return nil
}

// observeTotalProviders is called by the OTel SDK to report the total provider count.
func (r *Recorder) observeTotalProviders(_ context.Context, observer metric.Int64Observer) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	observer.Observe(int64(r.totalProviders))
	return nil
}

// RecordScoreChange records a change in a provider's score component.
func (r *Recorder) RecordScoreChange(_ context.Context, provider string, scoreType scoringmetrics.ScoreType, _, newValue float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.scores[provider] == nil {
		r.scores[provider] = make(map[scoringmetrics.ScoreType]float64)
	}
	r.scores[provider][scoreType] = newValue
}

// RecordEffectiveScore records the final computed score for a provider.
func (r *Recorder) RecordEffectiveScore(_ context.Context, provider string, score float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.effectiveScores[provider] = score
}

// RecordEvent records a scoring event.
func (r *Recorder) RecordEvent(ctx context.Context, provider string, eventType string, success bool) {
	r.eventsCounter.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String(scoringmetrics.Labels.Provider, provider),
			attribute.String(scoringmetrics.Labels.EventType, eventType),
			attribute.Bool(scoringmetrics.Labels.Success, success),
		),
	)
}

// RecordLatency records the latency of a provider operation.
func (r *Recorder) RecordLatency(ctx context.Context, provider string, operation string, duration time.Duration) {
	r.latencyHistogram.Record(ctx, duration.Seconds(),
		metric.WithAttributes(
			attribute.String(scoringmetrics.Labels.Provider, provider),
			attribute.String(scoringmetrics.Labels.Operation, operation),
		),
	)
}

// RecordStoreOperation records a storage backend operation.
func (r *Recorder) RecordStoreOperation(ctx context.Context, storeName string, operation string, duration time.Duration, err error) {
	r.storeOpsCounter.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String(scoringmetrics.Labels.Store, storeName),
			attribute.String(scoringmetrics.Labels.Operation, operation),
			attribute.Bool(scoringmetrics.Labels.Success, err == nil),
		),
	)
	r.storeLatencyHist.Record(ctx, duration.Seconds(),
		metric.WithAttributes(
			attribute.String(scoringmetrics.Labels.Store, storeName),
			attribute.String(scoringmetrics.Labels.Operation, operation),
		),
	)
}

// RecordCacheHit records a cache hit or miss event.
func (r *Recorder) RecordCacheHit(ctx context.Context, storeName string, hit bool) {
	r.cacheHitsCounter.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String(scoringmetrics.Labels.Store, storeName),
			attribute.Bool(scoringmetrics.Labels.CacheHit, hit),
		),
	)
}

// RecordProviderRank records the current rank of a provider.
func (r *Recorder) RecordProviderRank(_ context.Context, provider string, rank int, totalProviders int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.providerRanks[provider] = rank
	r.totalProviders = totalProviders
}

// Ensure Recorder implements scoringmetrics.Recorder at compile time.
var _ scoringmetrics.Recorder = (*Recorder)(nil)
