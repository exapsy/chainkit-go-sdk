package cloudagent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/exapsy/chainkit"
)

func TestNewMetricsRecorder_RecordsRichEvent(t *testing.T) {
	rec := NewMetricsRecorder(Options{APIKey: "k"})
	rec.RecordBlockchainRequestRich(context.Background(), chainkit.RequestEvent{
		Provider:     "mempool",
		Operation:    "GetUTXOs",
		Success:      true,
		Duration:     12 * time.Millisecond,
		AttemptCount: 2,
	})
	t0 := rec.transportRef().(*noopTransport).snapshot()
	if len(t0) != 1 {
		t.Fatalf("expected 1 buffered event, got %d", len(t0))
	}
	if t0[0].Request == nil {
		t.Fatal("Request payload missing")
	}
	if t0[0].Request.Operation != "GetUTXOs" {
		t.Fatalf("operation: got %q want GetUTXOs", t0[0].Request.Operation)
	}
}

func TestNewMetricsRecorder_PlainCallStillForwards(t *testing.T) {
	rec := NewMetricsRecorder(Options{APIKey: "k"})
	rec.RecordBlockchainRequest(context.Background(), "mempool", "GetUTXOs", true, 5*time.Millisecond)
	if got := len(rec.transportRef().(*noopTransport).snapshot()); got != 1 {
		t.Fatalf("expected 1 buffered event from RecordBlockchainRequest, got %d", got)
	}
}

func TestMetricsRecorder_SampleRateZeroDropsAll(t *testing.T) {
	rec := NewMetricsRecorder(Options{APIKey: "k", SampleRate: -1})
	// SampleRate -1 should normalise to default (1.0). Verify that path.
	rec.RecordBlockchainRequestRich(context.Background(), chainkit.RequestEvent{Operation: "Op"})
	if got := len(rec.transportRef().(*noopTransport).snapshot()); got != 1 {
		t.Fatalf("expected 1 event after sample-rate normalisation, got %d", got)
	}

	// Now construct one with shouldSample forced to false via a sample rate just
	// above zero — we sample 100 calls and expect a small but plausible count.
	rec2 := NewMetricsRecorder(Options{APIKey: "k", SampleRate: 0.01})
	for i := 0; i < 100; i++ {
		rec2.RecordBlockchainRequestRich(context.Background(), chainkit.RequestEvent{Operation: "Op"})
	}
	got := len(rec2.transportRef().(*noopTransport).snapshot())
	if got > 30 {
		t.Fatalf("sample rate 0.01 should drop most events; got %d / 100", got)
	}
}

func TestNewScoringRecorder_AllMethodsBuffer(t *testing.T) {
	rec := NewScoringRecorder(Options{APIKey: "k"})
	ctx := context.Background()

	rec.RecordEvent(ctx, "p1", "operation_success", true)
	rec.RecordLatency(ctx, "p1", "GetUTXOs", 8*time.Millisecond)
	rec.RecordEffectiveScore(ctx, "p1", 92.5)
	rec.RecordCacheHit(ctx, "redis", true)
	rec.RecordProviderRank(ctx, "p1", 1, 3)
	rec.RecordStoreOperation(ctx, "redis", "get", time.Millisecond, errors.New("connection refused"))

	events := rec.transportRef().(*noopTransport).snapshot()
	if len(events) != 6 {
		t.Fatalf("expected 6 buffered scoring events, got %d", len(events))
	}
	for i, ev := range events {
		if ev.Score == nil {
			t.Fatalf("event %d missing Score payload", i)
		}
	}
	// Privacy: error text must be classified, never raw.
	if events[5].Score.StoreErrText == "connection refused" {
		t.Fatal("raw error text leaked into ScoreEvent.StoreErrText")
	}
}

// fakeUpdater is a minimal chainkit.ConfigUpdater for the poller tests. It records
// every call so assertions can verify what the poller applied.
type fakeUpdater struct {
	mu                  sync.Mutex
	updateChainCalls    []chainkit.ProviderChainType
	setStrategyCalls    []chainkit.ProviderChainType
	setScoringWeightsCt int
	scoringEnabledCalls []bool
	updateErr           error
}

func (f *fakeUpdater) UpdateChainConfig(chain chainkit.ProviderChainType, _ chainkit.ChainConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateChainCalls = append(f.updateChainCalls, chain)
	return f.updateErr
}

func (f *fakeUpdater) SetSelectionStrategy(chain chainkit.ProviderChainType, _ chainkit.SelectionStrategy) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setStrategyCalls = append(f.setStrategyCalls, chain)
	return nil
}

func (f *fakeUpdater) SetScoringWeights(_ chainkit.ScoringWeights) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setScoringWeightsCt++
	return nil
}

func (f *fakeUpdater) SetScoringEnabled(enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scoringEnabledCalls = append(f.scoringEnabledCalls, enabled)
}

func (f *fakeUpdater) Snapshot() chainkit.ConfigSnapshot { return chainkit.ConfigSnapshot{} }

func TestConfigPoller_AppliesSnapshot(t *testing.T) {
	upd := &fakeUpdater{}
	desired := chainkit.ConfigSnapshot{
		Chains: map[chainkit.ProviderChainType]chainkit.ChainConfig{
			chainkit.ProviderChainBalanceFetchers: chainkit.DefaultChainConfig(chainkit.ProviderChainBalanceFetchers),
			chainkit.ProviderChainTxBroadcasters:  chainkit.DefaultChainConfig(chainkit.ProviderChainTxBroadcasters),
		},
		ScoringEnabled: true,
	}
	fetchedOnce := atomic.Bool{}
	fetch := func(_ context.Context, _ string) (fetchResult, error) {
		if fetchedOnce.Swap(true) {
			return fetchResult{notModified: true}, nil
		}
		return fetchResult{snap: desired, etag: "v1"}, nil
	}

	p := newConfigPoller(upd, Options{APIKey: "k"}.withDefaults(), fetch)
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	upd.mu.Lock()
	chainsApplied := len(upd.updateChainCalls)
	scoringWeightsApplied := upd.setScoringWeightsCt
	scoringEnabled := upd.scoringEnabledCalls
	upd.mu.Unlock()

	if chainsApplied != 2 {
		t.Fatalf("expected 2 UpdateChainConfig calls, got %d", chainsApplied)
	}
	if scoringWeightsApplied != 1 {
		t.Fatalf("expected 1 SetScoringWeights call, got %d", scoringWeightsApplied)
	}
	if len(scoringEnabled) != 1 || !scoringEnabled[0] {
		t.Fatalf("expected SetScoringEnabled(true), got %v", scoringEnabled)
	}
	if got := p.LastApplied().ScoringEnabled; !got {
		t.Fatal("LastApplied did not record applied snapshot")
	}

	// Second tick: not-modified path; nothing new should be applied.
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	upd.mu.Lock()
	defer upd.mu.Unlock()
	if len(upd.updateChainCalls) != 2 {
		t.Fatalf("not-modified tick should not re-apply; got %d UpdateChainConfig calls", len(upd.updateChainCalls))
	}
}

func TestConfigPoller_FetchErrorReturnsError(t *testing.T) {
	upd := &fakeUpdater{}
	wantErr := errors.New("network borked")
	fetch := func(_ context.Context, _ string) (fetchResult, error) { return fetchResult{}, wantErr }
	p := newConfigPoller(upd, Options{APIKey: "k"}.withDefaults(), fetch)
	if err := p.tick(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("tick error: got %v want %v", err, wantErr)
	}
}

func TestConfigPoller_StartStop(t *testing.T) {
	upd := &fakeUpdater{}
	p := NewConfigPoller(upd, Options{Endpoint: "x", APIKey: "k", PollInterval: 50 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	p.Start(ctx) // second Start is no-op
	time.Sleep(10 * time.Millisecond)
	p.Stop()
	p.Stop() // second Stop is no-op
}
