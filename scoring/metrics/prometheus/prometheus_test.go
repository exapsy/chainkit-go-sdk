package prometheus_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/exapsy/chainkit/scoring/metrics"
	promrecorder "github.com/exapsy/chainkit/scoring/metrics/prometheus"
)

// newTestRecorder returns a Recorder backed by a fresh isolated registry.
func newTestRecorder(t *testing.T) *promrecorder.Recorder {
	t.Helper()
	reg := prometheus.NewRegistry()
	return promrecorder.NewRecorder(promrecorder.Config{
		Namespace: "test",
		Subsystem: "scoring",
		Registry:  reg,
	})
}

func TestRecorder_ImplementsInterface(t *testing.T) {
	var _ metrics.Recorder = promrecorder.NewRecorder(promrecorder.DefaultConfig())
}

func TestRecorder_RecordScoreChange(t *testing.T) {
	reg := prometheus.NewRegistry()
	rec := promrecorder.NewRecorder(promrecorder.Config{
		Namespace: "test",
		Subsystem: "scoring",
		Registry:  reg,
	})

	ctx := context.Background()
	rec.RecordScoreChange(ctx, "mempool", metrics.ScoreTypeEffective, 100.0, 85.0)

	count, err := testutil.GatherAndCount(reg)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if count == 0 {
		t.Fatal("expected at least one metric family")
	}
}

func TestRecorder_RecordEffectiveScore(t *testing.T) {
	rec := newTestRecorder(t)
	ctx := context.Background()

	// Should not panic
	rec.RecordEffectiveScore(ctx, "blockcypher", 72.5)
	rec.RecordEffectiveScore(ctx, "mempool", 95.0)
}

func TestRecorder_RecordEvent(t *testing.T) {
	rec := newTestRecorder(t)
	ctx := context.Background()

	rec.RecordEvent(ctx, "mempool", "operation_success", true)
	rec.RecordEvent(ctx, "blockcypher", "healthcheck_failed", false)
	rec.RecordEvent(ctx, "mempool", "rate_limited", false)
}

func TestRecorder_RecordLatency(t *testing.T) {
	rec := newTestRecorder(t)
	ctx := context.Background()

	rec.RecordLatency(ctx, "mempool", "get_transaction", 42*time.Millisecond)
	rec.RecordLatency(ctx, "blockcypher", "broadcast", 200*time.Millisecond)
}

func TestRecorder_RecordStoreOperation(t *testing.T) {
	rec := newTestRecorder(t)
	ctx := context.Background()

	rec.RecordStoreOperation(ctx, "redis", "get_score", 1*time.Millisecond, nil)
	rec.RecordStoreOperation(ctx, "postgres", "set_score", 5*time.Millisecond, nil)
	rec.RecordStoreOperation(ctx, "redis", "get_score", 0, errFake)
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
	rec.RecordProviderRank(ctx, "blockstream", 3, 3)
}

func TestDefaultConfig(t *testing.T) {
	cfg := promrecorder.DefaultConfig()
	if cfg.Namespace != "chainkit" {
		t.Errorf("namespace = %q, want %q", cfg.Namespace, "chainkit")
	}
	if cfg.Subsystem != "scoring" {
		t.Errorf("subsystem = %q, want %q", cfg.Subsystem, "scoring")
	}
	if len(cfg.LatencyBuckets) == 0 {
		t.Error("expected non-empty latency buckets")
	}
}

// errFake is a sentinel error for testing error-path recording.
var errFake = fakeError("connection refused")

type fakeError string

func (e fakeError) Error() string { return string(e) }
