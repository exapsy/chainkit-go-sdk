package cloudagent

import (
	"context"
	"math/rand"
	"time"

	"github.com/exapsy/chainkit"
	scoringmetrics "github.com/exapsy/chainkit/scoring/metrics"
)

// MetricsRecorder is the cloudagent's implementation of chainkit.RichMetricsRecorder.
// It receives one event per top-level blockchain operation and forwards it to the
// configured transport. Calls are non-blocking; the transport is responsible for
// any buffering.
type MetricsRecorder struct {
	opts      Options
	transport transport
	rand      *rand.Rand
}

// NewMetricsRecorder constructs a MetricsRecorder. The recorder satisfies both
// chainkit.MetricsRecorder and chainkit.RichMetricsRecorder; pass it to
// chainkit.NewMixedProvidersBuilder().WithMetricsRecorder(...).
//
// When opts.Endpoint is set, the recorder pushes telemetry to chainkit-cloud
// via a ring-buffered HTTP transport (drop-oldest on overflow, exponential
// backoff on errors, never blocks the call site). When opts.Endpoint is empty,
// the recorder retains an in-memory transport — useful for tests and dev
// without a cloud endpoint.
func NewMetricsRecorder(opts Options) *MetricsRecorder {
	opts = opts.withDefaults()
	return newMetricsRecorder(opts, selectTransport(opts))
}

// newMetricsRecorder is the internal constructor that lets tests inject a custom
// transport.
func newMetricsRecorder(opts Options, t transport) *MetricsRecorder {
	return &MetricsRecorder{
		opts:      opts,
		transport: t,
		// Seed from the wall clock; sampling is stochastic, not security-sensitive.
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// RecordBlockchainRequest satisfies the older chainkit.MetricsRecorder interface.
// The SDK only calls this when the recorder does NOT also satisfy RichMetricsRecorder
// — but since *MetricsRecorder satisfies both, this method is effectively unreachable
// from the SDK. We still implement it so the type satisfies MetricsRecorder.
func (m *MetricsRecorder) RecordBlockchainRequest(ctx context.Context, provider, operation string, success bool, duration time.Duration) {
	m.RecordBlockchainRequestRich(ctx, chainkit.RequestEvent{
		Provider:  provider,
		Operation: operation,
		Success:   success,
		Duration:  duration,
	})
}

// RecordBlockchainRequestRich forwards the event to the transport, applying any
// configured sample-rate downsampling.
func (m *MetricsRecorder) RecordBlockchainRequestRich(_ context.Context, e chainkit.RequestEvent) {
	if !m.shouldSample() {
		return
	}
	m.transport.Push(Event{
		CapturedAt: nowFunc(),
		Request:    &e,
	})
}

func (m *MetricsRecorder) shouldSample() bool {
	if m.opts.SampleRate >= 1.0 {
		return true
	}
	if m.opts.SampleRate <= 0 {
		return false
	}
	return m.rand.Float64() < m.opts.SampleRate
}

// transportRef exposes the internal transport for tests in this package.
func (m *MetricsRecorder) transportRef() transport { return m.transport }

// ScoringRecorder is the cloudagent's implementation of scoring/metrics.Recorder.
// Pass it to scoring.WithMetrics(...) when constructing the scoring engine.
type ScoringRecorder struct {
	opts      Options
	transport transport
}

// NewScoringRecorder constructs a ScoringRecorder. Pushes telemetry through
// the same transport configuration as NewMetricsRecorder.
func NewScoringRecorder(opts Options) *ScoringRecorder {
	opts = opts.withDefaults()
	return newScoringRecorder(opts, selectTransport(opts))
}

// selectTransport returns the right transport for opts. With an endpoint, the
// HTTP transport; without, the in-memory noop transport so dev / tests keep
// working without a cloud target.
func selectTransport(opts Options) transport {
	if opts.Endpoint == "" {
		return newNoopTransport()
	}
	return newHTTPTransport(opts)
}

func newScoringRecorder(opts Options, t transport) *ScoringRecorder {
	return &ScoringRecorder{opts: opts, transport: t}
}

// transportRef exposes the internal transport for tests in this package.
func (s *ScoringRecorder) transportRef() transport { return s.transport }

func (s *ScoringRecorder) push(ev ScoreEvent) {
	s.transport.Push(Event{CapturedAt: nowFunc(), Score: &ev})
}

// RecordScoreChange satisfies scoring/metrics.Recorder.
func (s *ScoringRecorder) RecordScoreChange(_ context.Context, provider string, scoreType scoringmetrics.ScoreType, oldValue, newValue float64) {
	s.push(ScoreEvent{
		Provider:  provider,
		Kind:      ScoreEventKindScoreChange,
		ScoreType: string(scoreType),
		OldValue:  oldValue,
		NewValue:  newValue,
	})
}

// RecordEffectiveScore satisfies scoring/metrics.Recorder.
func (s *ScoringRecorder) RecordEffectiveScore(_ context.Context, provider string, score float64) {
	s.push(ScoreEvent{
		Provider:  provider,
		Kind:      ScoreEventKindEffective,
		ScoreType: string(scoringmetrics.ScoreTypeEffective),
		Score:     score,
	})
}

// RecordEvent satisfies scoring/metrics.Recorder.
func (s *ScoringRecorder) RecordEvent(_ context.Context, provider, eventType string, success bool) {
	s.push(ScoreEvent{
		Provider:  provider,
		Kind:      ScoreEventKindEvent,
		EventType: eventType,
		Success:   success,
	})
}

// RecordLatency satisfies scoring/metrics.Recorder.
func (s *ScoringRecorder) RecordLatency(_ context.Context, provider, operation string, duration time.Duration) {
	s.push(ScoreEvent{
		Provider:  provider,
		Kind:      ScoreEventKindLatency,
		Operation: operation,
		Latency:   duration,
	})
}

// RecordStoreOperation satisfies scoring/metrics.Recorder.
func (s *ScoringRecorder) RecordStoreOperation(_ context.Context, store, operation string, duration time.Duration, err error) {
	ev := ScoreEvent{
		Provider:  "",
		Kind:      ScoreEventKindStoreOp,
		Store:     store,
		Operation: operation,
		Latency:   duration,
		Success:   err == nil,
	}
	if err != nil {
		// Privacy: classify, never forward raw error text.
		ev.StoreErrText = "error"
	}
	s.push(ev)
}

// RecordCacheHit satisfies scoring/metrics.Recorder.
func (s *ScoringRecorder) RecordCacheHit(_ context.Context, store string, hit bool) {
	s.push(ScoreEvent{
		Kind:     ScoreEventKindCacheHit,
		Store:    store,
		CacheHit: hit,
	})
}

// RecordProviderRank satisfies scoring/metrics.Recorder.
func (s *ScoringRecorder) RecordProviderRank(_ context.Context, provider string, rank, totalProviders int) {
	s.push(ScoreEvent{
		Provider:       provider,
		Kind:           ScoreEventKindProviderRank,
		Rank:           rank,
		TotalProviders: totalProviders,
	})
}
