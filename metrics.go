package chainkit

import (
	"context"
	"errors"
	"strings"
	"time"
)

// MetricsRecorder receives one event per blockchain operation that the SDK dispatches
// through a provider chain. The recorder is invoked once per top-level call (e.g.,
// GetUTXOs, PushTx) regardless of how many providers were tried or how many retries
// were attempted internally.
//
// Implementations must be goroutine-safe and non-blocking — the SDK calls the recorder
// inline on the request path. Long-running work (network I/O, disk writes) should be
// dispatched to a background queue.
type MetricsRecorder interface {
	RecordBlockchainRequest(ctx context.Context, provider string, operation string, success bool, duration time.Duration)
}

// RichMetricsRecorder is an optional extension of MetricsRecorder that receives a
// structured RequestEvent with additional fields (attempt count, classified error).
// The SDK type-asserts on this interface; implementations that satisfy it receive
// RecordBlockchainRequestRich. Implementations that don't only receive the simpler
// RecordBlockchainRequest call.
//
// Adding this interface is non-breaking: existing MetricsRecorder implementations
// continue to work unchanged.
type RichMetricsRecorder interface {
	MetricsRecorder
	RecordBlockchainRequestRich(ctx context.Context, e RequestEvent)
}

// RequestEvent describes a single completed blockchain operation. The event is
// emitted once per top-level SDK call, after fallback and retry have run to
// completion (success or exhaustion).
//
// RequestEvent does not carry chain or network labels — those are provider-chain
// metadata that lives at a layer above the SDK (e.g., the cloudagent fills them in
// from its Options before forwarding the event to chainkit-cloud). This keeps the
// SDK chain-agnostic.
type RequestEvent struct {
	// Provider is the name of the provider that ultimately served the request, or
	// the empty string if every provider in the chain failed.
	Provider string

	// Operation is the SDK method name (e.g., "GetUTXOs", "PushTx", "DeriveAddress").
	Operation string

	// Success is true iff the operation returned without error.
	Success bool

	// Duration is the total wall-clock time from the SDK call entry to its return,
	// including time spent on failed providers and retry backoff. It is the latency
	// the caller actually observed.
	Duration time.Duration

	// AttemptCount is the total number of provider invocations made across the entire
	// fallback chain. A successful first call is 1; a call that retried twice on
	// provider A then succeeded on provider B is 4 (2 + 1 + 1).
	AttemptCount int

	// ErrorClass classifies the terminal error using a small enumerated vocabulary.
	// Empty when Success is true.
	//
	// Possible values:
	//   - "auth"        — credentials were rejected (wraps ErrAuthFailure)
	//   - "config"      — no provider configured for the requested capability
	//   - "timeout"     — context deadline exceeded or context cancelled
	//   - "rate_limit"  — provider returned a rate-limit signal
	//   - "other"       — any other error
	//
	// Raw error messages are intentionally not surfaced. Telemetry consumers receive
	// only the classification, never provider-specific error text or stack traces.
	ErrorClass string
}

// classifyError maps an error to one of the RequestEvent.ErrorClass enum values.
// Returns the empty string for nil errors.
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrAuthFailure) {
		return "auth"
	}
	if errors.Is(err, ErrProviderNotConfigured) {
		return "config"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "rate limit") || strings.Contains(msg, "429") || strings.Contains(msg, "too many requests") {
		return "rate_limit"
	}
	return "other"
}

// NoOpMetricsRecorder is a no-op implementation for when metrics are disabled.
// It satisfies MetricsRecorder but not RichMetricsRecorder, so the SDK falls back
// to the simple recording path.
type NoOpMetricsRecorder struct{}

func (n *NoOpMetricsRecorder) RecordBlockchainRequest(_ context.Context, _ string, _ string, _ bool, _ time.Duration) {
	// No-op
}
