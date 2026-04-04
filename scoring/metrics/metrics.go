package metrics

import (
	"context"
	"time"
)

// Recorder is the core interface for recording scoring metrics.
// Implementations should be thread-safe.
//
// The scoring engine uses this interface to report metrics about provider
// performance, score changes, and storage operations. By default, a no-op
// implementation is used. Users can plug in Prometheus, OpenTelemetry, or
// custom implementations.
type Recorder interface {
	// RecordScoreChange records a change in a provider's score component.
	// This is called when penalties or base scores are updated.
	//
	// Parameters:
	//   - provider: The provider name
	//   - scoreType: The type of score (base, health_penalty, etc.)
	//   - oldValue: Previous value
	//   - newValue: New value
	RecordScoreChange(ctx context.Context, provider string, scoreType ScoreType, oldValue, newValue float64)

	// RecordEffectiveScore records the final computed score for a provider.
	// This is the score after all penalties are applied.
	//
	// Parameters:
	//   - provider: The provider name
	//   - score: The effective score value
	RecordEffectiveScore(ctx context.Context, provider string, score float64)

	// RecordEvent records a scoring event (health check, operation success/failure, etc.).
	//
	// Parameters:
	//   - provider: The provider name
	//   - eventType: Type of event (e.g., "health_check_failed", "operation_success")
	//   - success: Whether the event was successful
	RecordEvent(ctx context.Context, provider string, eventType string, success bool)

	// RecordLatency records the latency of a provider operation.
	//
	// Parameters:
	//   - provider: The provider name
	//   - operation: Operation name (e.g., "get_transaction", "broadcast")
	//   - duration: How long the operation took
	RecordLatency(ctx context.Context, provider string, operation string, duration time.Duration)

	// RecordStoreOperation records a storage operation.
	//
	// Parameters:
	//   - store: Store name (e.g., "memory", "redis", "postgres", "hybrid")
	//   - operation: Operation name (e.g., "get", "set", "delete")
	//   - duration: How long the operation took
	//   - err: Error if operation failed, nil if successful
	RecordStoreOperation(ctx context.Context, store string, operation string, duration time.Duration, err error)

	// RecordCacheHit records cache hit/miss events (for hybrid stores).
	//
	// Parameters:
	//   - store: Store name
	//   - hit: true if cache hit, false if cache miss
	RecordCacheHit(ctx context.Context, store string, hit bool)

	// RecordProviderRank records the current rank of a provider.
	// Rank 1 is the best provider, rank N is the worst.
	//
	// Parameters:
	//   - provider: The provider name
	//   - rank: Current rank (1-based)
	//   - totalProviders: Total number of providers
	RecordProviderRank(ctx context.Context, provider string, rank int, totalProviders int)
}

// ScoreType represents the type of score component.
type ScoreType string

const (
	// ScoreTypeBase is the base score (usually 100.0)
	ScoreTypeBase ScoreType = "base"

	// ScoreTypeHealth is the health check penalty
	ScoreTypeHealth ScoreType = "health_penalty"

	// ScoreTypeLatency is the latency penalty
	ScoreTypeLatency ScoreType = "latency_penalty"

	// ScoreTypeError is the error penalty
	ScoreTypeError ScoreType = "error_penalty"

	// ScoreTypeRateLimit is the rate limit penalty
	ScoreTypeRateLimit ScoreType = "rate_limit_penalty"

	// ScoreTypeEffective is the final computed score
	ScoreTypeEffective ScoreType = "effective"
)

// Labels provides standard label names for consistency across implementations.
// Use these constants when implementing custom recorders to ensure compatibility.
var Labels = struct {
	Provider  string
	Operation string
	EventType string
	ScoreType string
	Store     string
	Success   string
	CacheHit  string
}{
	Provider:  "provider",
	Operation: "operation",
	EventType: "event_type",
	ScoreType: "score_type",
	Store:     "store",
	Success:   "success",
	CacheHit:  "hit",
}

// NoOpRecorder is the default recorder that does nothing.
// Used when metrics are not configured.
//
// This implementation has zero overhead and is safe for production use
// when metrics are not needed.
type NoOpRecorder struct{}

// Ensure NoOpRecorder implements Recorder at compile time
var _ Recorder = (*NoOpRecorder)(nil)

// RecordScoreChange does nothing
func (n *NoOpRecorder) RecordScoreChange(ctx context.Context, provider string, scoreType ScoreType, oldValue, newValue float64) {
}

// RecordEffectiveScore does nothing
func (n *NoOpRecorder) RecordEffectiveScore(ctx context.Context, provider string, score float64) {
}

// RecordEvent does nothing
func (n *NoOpRecorder) RecordEvent(ctx context.Context, provider string, eventType string, success bool) {
}

// RecordLatency does nothing
func (n *NoOpRecorder) RecordLatency(ctx context.Context, provider string, operation string, duration time.Duration) {
}

// RecordStoreOperation does nothing
func (n *NoOpRecorder) RecordStoreOperation(ctx context.Context, store string, operation string, duration time.Duration, err error) {
}

// RecordCacheHit does nothing
func (n *NoOpRecorder) RecordCacheHit(ctx context.Context, store string, hit bool) {
}

// RecordProviderRank does nothing
func (n *NoOpRecorder) RecordProviderRank(ctx context.Context, provider string, rank int, totalProviders int) {
}
