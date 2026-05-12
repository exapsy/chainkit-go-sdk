package cloudagent

import (
	"context"
	"sync"
	"time"

	"github.com/exapsy/chainkit"
	scoringmetrics "github.com/exapsy/chainkit/scoring/metrics"
)

// Event is the cloudagent's internal envelope around an SDK telemetry event.
// The wire format consumed by chainkit-cloud is defined separately (see
// docs/cloud-ingest-schema.md); transport implementations are responsible for
// mapping Event to that wire format.
type Event struct {
	// Capturedat is the wall-clock time at which the event was emitted by the SDK.
	CapturedAt time.Time

	// One of these is set, identifying the event payload type.
	// Transport implementations switch on whichever is non-nil.
	Request *chainkit.RequestEvent
	Score   *ScoreEvent
}

// ScoreEvent is the cloudagent representation of a scoring telemetry event.
// It mirrors the relevant fields of scoring/metrics.Recorder methods, normalised
// into a single struct so transports don't need to track which Record* call
// produced it.
type ScoreEvent struct {
	Provider       string
	Kind           ScoreEventKind
	EventType      string  // for Kind == ScoreEventKindEvent
	ScoreType      string  // for Kind == ScoreEventKindScoreChange / ScoreEventKindEffective
	Operation      string  // for Kind == ScoreEventKindLatency
	Store          string  // for Kind == ScoreEventKindStoreOp / ScoreEventKindCacheHit
	OldValue       float64
	NewValue       float64
	Score          float64
	Latency        time.Duration
	Success        bool
	CacheHit       bool
	Rank           int
	TotalProviders int
	StoreErrText   string // serialised classification, never raw error text in prod
}

// ScoreEventKind enumerates which RecordX call produced the event.
type ScoreEventKind string

const (
	ScoreEventKindScoreChange  ScoreEventKind = "score_change"
	ScoreEventKindEffective    ScoreEventKind = "effective"
	ScoreEventKindEvent        ScoreEventKind = "event"
	ScoreEventKindLatency      ScoreEventKind = "latency"
	ScoreEventKindStoreOp      ScoreEventKind = "store_op"
	ScoreEventKindCacheHit     ScoreEventKind = "cache_hit"
	ScoreEventKindProviderRank ScoreEventKind = "provider_rank"
)

// transport is the cloudagent's internal sink for events. The skeleton ships
// only an in-memory implementation; the real ring-buffer-backed HTTP transport
// lands in P2 of the chainkit-cloud build sequence.
//
// Transports must be goroutine-safe and non-blocking — Push is invoked on the
// SDK's hot path.
type transport interface {
	Push(Event)
	Stop()
}

// noopTransport stores events in memory and exposes them for tests. It applies no
// backpressure: Push always returns immediately, regardless of how many events are
// already buffered. This is fine for the skeleton; the real transport will impose
// the documented BufferSize ring-buffer with drop-oldest semantics.
type noopTransport struct {
	mu     sync.Mutex
	events []Event
}

func newNoopTransport() *noopTransport { return &noopTransport{} }

func (t *noopTransport) Push(e Event) {
	t.mu.Lock()
	t.events = append(t.events, e)
	t.mu.Unlock()
}

func (t *noopTransport) Stop() {}

// snapshot returns a copy of the buffered events. Tests use this to assert that
// the SDK forwarded the expected events into the agent.
func (t *noopTransport) snapshot() []Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Event, len(t.events))
	copy(out, t.events)
	return out
}

// Compile-time assertions that the recorders produced by this package satisfy
// the SDK's required interfaces.
var (
	_ chainkit.RichMetricsRecorder = (*MetricsRecorder)(nil)
	_ scoringmetrics.Recorder      = (*ScoringRecorder)(nil)
)

// nowFunc is the clock used by the agent. Tests override it to make tests deterministic.
var nowFunc = time.Now

// Compile-time use to silence unused-import warnings on certain builds.
var _ = context.Background
