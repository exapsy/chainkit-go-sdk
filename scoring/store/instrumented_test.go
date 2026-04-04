package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/exapsy/chainkit/scoring/metrics"
	"github.com/exapsy/chainkit/scoring/store"
)

// captureRecorder records every call made to it so tests can assert on them.
type captureRecorder struct {
	storeOps  []storeOp
	cacheHits []cacheHit
}

type storeOp struct {
	store     string
	operation string
	duration  time.Duration
	err       error
}

type cacheHit struct {
	store string
	hit   bool
}

func (r *captureRecorder) RecordScoreChange(_ context.Context, _ string, _ metrics.ScoreType, _, _ float64) {
}
func (r *captureRecorder) RecordEffectiveScore(_ context.Context, _ string, _ float64) {}
func (r *captureRecorder) RecordEvent(_ context.Context, _ string, _ string, _ bool)   {}
func (r *captureRecorder) RecordLatency(_ context.Context, _ string, _ string, _ time.Duration) {
}
func (r *captureRecorder) RecordProviderRank(_ context.Context, _ string, _ int, _ int) {}

func (r *captureRecorder) RecordStoreOperation(_ context.Context, s string, op string, d time.Duration, err error) {
	r.storeOps = append(r.storeOps, storeOp{store: s, operation: op, duration: d, err: err})
}

func (r *captureRecorder) RecordCacheHit(_ context.Context, s string, hit bool) {
	r.cacheHits = append(r.cacheHits, cacheHit{store: s, hit: hit})
}

var _ metrics.Recorder = (*captureRecorder)(nil)

// --- Tests ---

func TestInstrumentedStore_Name(t *testing.T) {
	mem := store.NewMemoryStore()
	s := store.NewInstrumentedStore(mem, &metrics.NoOpRecorder{})
	if s.Name() != "instrumented/memory" {
		t.Errorf("Name() = %q, want %q", s.Name(), "instrumented/memory")
	}
}

func TestInstrumentedStore_NilRecorderFallsBack(t *testing.T) {
	mem := store.NewMemoryStore()
	// nil recorder must not panic
	s := store.NewInstrumentedStore(mem, nil)
	ctx := context.Background()
	_ = s.Ping(ctx)
}

func TestInstrumentedStore_RecordsGetScore(t *testing.T) {
	rec := &captureRecorder{}
	s := store.NewInstrumentedStore(store.NewMemoryStore(), rec)
	ctx := context.Background()

	_, _ = s.GetScore(ctx, "mempool")

	if len(rec.storeOps) != 1 {
		t.Fatalf("expected 1 store op, got %d", len(rec.storeOps))
	}
	op := rec.storeOps[0]
	if op.operation != "get_score" {
		t.Errorf("operation = %q, want %q", op.operation, "get_score")
	}
	if op.err != nil {
		t.Errorf("unexpected error: %v", op.err)
	}
}

func TestInstrumentedStore_RecordsSetScore(t *testing.T) {
	rec := &captureRecorder{}
	s := store.NewInstrumentedStore(store.NewMemoryStore(), rec)
	ctx := context.Background()

	data := &store.ProviderScoreData{Name: "mempool", BaseScore: 100}
	_ = s.SetScore(ctx, data)

	if len(rec.storeOps) != 1 {
		t.Fatalf("expected 1 store op, got %d", len(rec.storeOps))
	}
	if rec.storeOps[0].operation != "set_score" {
		t.Errorf("operation = %q", rec.storeOps[0].operation)
	}
}

func TestInstrumentedStore_RecordsErrors(t *testing.T) {
	boom := &errorStore{err: errors.New("boom")}
	rec := &captureRecorder{}
	s := store.NewInstrumentedStore(boom, rec)
	ctx := context.Background()

	_, err := s.GetScore(ctx, "x")
	if err == nil {
		t.Fatal("expected error")
	}
	if rec.storeOps[0].err == nil {
		t.Error("expected error to be recorded")
	}
}

func TestInstrumentedStore_RecordsAllOperations(t *testing.T) {
	rec := &captureRecorder{}
	s := store.NewInstrumentedStore(store.NewMemoryStore(), rec)
	ctx := context.Background()
	data := &store.ProviderScoreData{Name: "p", BaseScore: 100}

	_ = s.SetScore(ctx, data)
	_, _ = s.GetScore(ctx, "p")
	_, _ = s.GetAllScores(ctx)
	_ = s.SetScores(ctx, []*store.ProviderScoreData{data})
	_ = s.DeleteScore(ctx, "p")
	_ = s.SetLatencyStats(ctx, &store.LatencyStatsData{})
	_, _ = s.GetLatencyStats(ctx)

	wantOps := []string{
		"set_score", "get_score", "get_all_scores",
		"set_scores", "delete_score", "set_latency_stats", "get_latency_stats",
	}
	if len(rec.storeOps) != len(wantOps) {
		t.Fatalf("got %d ops, want %d", len(rec.storeOps), len(wantOps))
	}
	for i, want := range wantOps {
		if got := rec.storeOps[i].operation; got != want {
			t.Errorf("op[%d] = %q, want %q", i, got, want)
		}
	}
}

func TestInstrumentedStore_DurationIsPositive(t *testing.T) {
	rec := &captureRecorder{}
	s := store.NewInstrumentedStore(store.NewMemoryStore(), rec)
	ctx := context.Background()

	_, _ = s.GetScore(ctx, "x")

	if rec.storeOps[0].duration < 0 {
		t.Error("duration should be >= 0")
	}
}

// errorStore always returns an error for every operation.
type errorStore struct {
	err error
}

func (e *errorStore) Name() string                                                    { return "error" }
func (e *errorStore) GetScore(_ context.Context, _ string) (*store.ProviderScoreData, error) {
	return nil, e.err
}
func (e *errorStore) SetScore(_ context.Context, _ *store.ProviderScoreData) error { return e.err }
func (e *errorStore) GetAllScores(_ context.Context) ([]*store.ProviderScoreData, error) {
	return nil, e.err
}
func (e *errorStore) DeleteScore(_ context.Context, _ string) error { return e.err }
func (e *errorStore) SetScores(_ context.Context, _ []*store.ProviderScoreData) error {
	return e.err
}
func (e *errorStore) GetLatencyStats(_ context.Context) (*store.LatencyStatsData, error) {
	return nil, e.err
}
func (e *errorStore) SetLatencyStats(_ context.Context, _ *store.LatencyStatsData) error {
	return e.err
}
func (e *errorStore) Close() error                    { return e.err }
func (e *errorStore) Ping(_ context.Context) error   { return e.err }
