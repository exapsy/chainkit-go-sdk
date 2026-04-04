package otel_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/exapsy/chainkit/scoring/metrics"
	otelrecorder "github.com/exapsy/chainkit/scoring/metrics/otel"
)

// newTestRecorder returns a Recorder backed by the OTel no-op provider.
func newTestRecorder(t *testing.T) *otelrecorder.Recorder {
	t.Helper()
	rec, err := otelrecorder.NewRecorder(otelrecorder.Config{
		MeterName:     "test.scoring",
		MeterProvider: noop.NewMeterProvider(),
	})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	return rec
}

func TestRecorder_ImplementsInterface(t *testing.T) {
	rec := newTestRecorder(t)
	var _ metrics.Recorder = rec
}

func TestNewRecorder_DefaultConfig(t *testing.T) {
	// Uses global noop provider by default (no OTel SDK configured in test).
	rec, err := otelrecorder.NewRecorder(otelrecorder.DefaultConfig())
	if err != nil {
		t.Fatalf("NewRecorder with default config: %v", err)
	}
	if rec == nil {
		t.Fatal("expected non-nil recorder")
	}
}

func TestRecorder_RecordScoreChange(t *testing.T) {
	rec := newTestRecorder(t)
	ctx := context.Background()

	// Should not panic; no-op provider discards all observations.
	rec.RecordScoreChange(ctx, "mempool", metrics.ScoreTypeEffective, 100.0, 85.0)
	rec.RecordScoreChange(ctx, "mempool", metrics.ScoreTypeHealth, 0.0, 5.0)
}

func TestRecorder_RecordEffectiveScore(t *testing.T) {
	rec := newTestRecorder(t)
	ctx := context.Background()

	rec.RecordEffectiveScore(ctx, "mempool", 85.0)
	rec.RecordEffectiveScore(ctx, "blockcypher", 70.0)
}

func TestRecorder_RecordEvent(t *testing.T) {
	rec := newTestRecorder(t)
	ctx := context.Background()

	rec.RecordEvent(ctx, "mempool", "operation_success", true)
	rec.RecordEvent(ctx, "blockcypher", "healthcheck_failed", false)
}

func TestRecorder_RecordLatency(t *testing.T) {
	rec := newTestRecorder(t)
	ctx := context.Background()

	rec.RecordLatency(ctx, "mempool", "get_transaction", 42*time.Millisecond)
}

func TestRecorder_RecordStoreOperation(t *testing.T) {
	rec := newTestRecorder(t)
	ctx := context.Background()

	rec.RecordStoreOperation(ctx, "redis", "get_score", 1*time.Millisecond, nil)
	rec.RecordStoreOperation(ctx, "redis", "get_score", 0, fakeError("err"))
}

func TestRecorder_RecordCacheHit(t *testing.T) {
	rec := newTestRecorder(t)
	ctx := context.Background()

	rec.RecordCacheHit(ctx, "hybrid(postgres+redis)", true)
	rec.RecordCacheHit(ctx, "hybrid(postgres+redis)", false)
}

func TestRecorder_RecordProviderRank(t *testing.T) {
	rec := newTestRecorder(t)
	ctx := context.Background()

	rec.RecordProviderRank(ctx, "mempool", 1, 3)
	rec.RecordProviderRank(ctx, "blockcypher", 2, 3)
}

func TestRecorder_ConcurrentAccess(t *testing.T) {
	rec := newTestRecorder(t)
	ctx := context.Background()

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(i int) {
			rec.RecordScoreChange(ctx, "mempool", metrics.ScoreTypeEffective, float64(i), float64(i+1))
			rec.RecordEffectiveScore(ctx, "mempool", float64(i))
			rec.RecordProviderRank(ctx, "mempool", i%3+1, 3)
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

type fakeError string

func (e fakeError) Error() string { return string(e) }
