package chainkit

import (
	"context"
	"time"
)

// MetricsRecorder interface that matches the existing metrics.Recorder interface
// This allows the chainkit package to work with the existing metrics service
type MetricsRecorder interface {
	RecordBlockchainRequest(ctx context.Context, provider string, operation string, success bool, duration time.Duration)
}

// NoOpMetricsRecorder is a no-op implementation for when metrics are disabled
type NoOpMetricsRecorder struct{}

func (n *NoOpMetricsRecorder) RecordBlockchainRequest(_ context.Context, _ string, _ string, _ bool, _ time.Duration) {
	// No-op
}
