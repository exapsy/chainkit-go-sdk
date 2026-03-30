package chainkit

import (
	"context"
	"time"
)

type debugContextKey struct{}

// debugState holds mutable state accumulated during a RunOp traversal.
type debugState struct {
	failedProviders []string
}

// WithDebugContext attaches a debug-accumulator to ctx. Call this before
// dispatching an operation through [MixedProviders] if you want
// [ExtractDebugInfo] to return the full provider trail.
func WithDebugContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, debugContextKey{}, &debugState{})
}

// recordFailedProvider appends name to the failed-provider list stored in ctx
// by [WithDebugContext]. No-op if the context carries no debug state.
func recordFailedProvider(ctx context.Context, name string) {
	if s, ok := ctx.Value(debugContextKey{}).(*debugState); ok {
		s.failedProviders = append(s.failedProviders, name)
	}
}

// DebugInfo represents blockchain provider debug information extracted from
// a context that was prepared with [WithDebugContext].
type DebugInfo struct {
	// Provider is the name of the provider that successfully handled the request.
	Provider string
	// FailedProviders lists providers that were tried and failed before the
	// successful one. Empty when the first provider succeeded or when the
	// context was not prepared with [WithDebugContext].
	FailedProviders []string
	ProcessedAt     time.Time
}

// ExtractDebugInfo extracts blockchain provider information from context.
// For FailedProviders to be populated the context must have been prepared
// with [WithDebugContext] before the operation was dispatched.
func ExtractDebugInfo(ctx context.Context) *DebugInfo {
	providerName, ok := GetProviderName(ctx)
	if !ok {
		providerName = "unknown"
	}

	var failed []string
	if s, ok := ctx.Value(debugContextKey{}).(*debugState); ok && len(s.failedProviders) > 0 {
		failed = make([]string, len(s.failedProviders))
		copy(failed, s.failedProviders)
	}

	return &DebugInfo{
		Provider:        providerName,
		FailedProviders: failed,
		ProcessedAt:     time.Now(),
	}
}
