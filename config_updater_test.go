package chainkit

import (
	"errors"
	"testing"
	"time"

	"github.com/exapsy/chainkit/scoring"
)

// newTestMixedProviders constructs a MixedProviders with one provider on each chain
// that the test cares about. Pass the chain types to populate.
func newTestMixedProviders(t *testing.T, chains ...ProviderChainType) *MixedProviders {
	t.Helper()
	m := &MixedProviders{}
	all := map[ProviderChainType]**providerManager{
		ProviderChainAddressGenerators: &m.addressGenerators,
		ProviderChainAddressValidators: &m.addressValidators,
		ProviderChainFeeEstimators:     &m.feeEstimators,
		ProviderChainFeeRecommenders:   &m.feeRecommenders,
		ProviderChainTxBroadcasters:    &m.txBroadcasters,
		ProviderChainUTXOFetchers:      &m.utxoFetchers,
		ProviderChainTxAssemblers:      &m.txAssemblers,
		ProviderChainTxSizers:          &m.txSizers,
		ProviderChainTxSigners:         &m.txSigners,
		ProviderChainTxStatusFetchers:  &m.txStatusFetchers,
		ProviderChainBalanceFetchers:   &m.balanceFetchers,
		ProviderChainRateFetchers:      &m.rateFetchers,
	}
	for _, chain := range chains {
		slot, ok := all[chain]
		if !ok {
			t.Fatalf("unknown chain %q", chain)
		}
		pm := newProviderManager(DefaultChainConfig(chain))
		pm.addProvider(struct{}{}, 1, "fake")
		*slot = pm
	}
	return m
}

func TestConfigUpdater_UpdateChainConfig(t *testing.T) {
	m := newTestMixedProviders(t, ProviderChainBalanceFetchers)

	cfg := DefaultChainConfig(ProviderChainBalanceFetchers)
	cfg.Timeout = 99 * time.Second
	cfg.MaxConcurrency = 7

	if err := m.UpdateChainConfig(ProviderChainBalanceFetchers, cfg); err != nil {
		t.Fatalf("UpdateChainConfig: %v", err)
	}
	got := m.balanceFetchers.config
	if got.Timeout != 99*time.Second {
		t.Fatalf("Timeout: got %v want 99s", got.Timeout)
	}
	if got.MaxConcurrency != 7 {
		t.Fatalf("MaxConcurrency: got %d want 7", got.MaxConcurrency)
	}
}

func TestConfigUpdater_UpdateChainConfig_UnknownChain(t *testing.T) {
	m := newTestMixedProviders(t)
	err := m.UpdateChainConfig(ProviderChainType("not-a-chain"), DefaultChainConfig(ProviderChainBalanceFetchers))
	if err == nil {
		t.Fatal("expected error for unknown chain")
	}
}

func TestConfigUpdater_UpdateChainConfig_UnconfiguredChain(t *testing.T) {
	// No chains registered — every manager is nil.
	m := newTestMixedProviders(t)
	err := m.UpdateChainConfig(ProviderChainBalanceFetchers, DefaultChainConfig(ProviderChainBalanceFetchers))
	if !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("expected ErrProviderNotConfigured, got %v", err)
	}
}

func TestConfigUpdater_SetSelectionStrategy(t *testing.T) {
	m := newTestMixedProviders(t, ProviderChainTxBroadcasters)
	if err := m.SetSelectionStrategy(ProviderChainTxBroadcasters, SelectionStrategyRoundRobin); err != nil {
		t.Fatalf("SetSelectionStrategy: %v", err)
	}
	if got := m.txBroadcasters.GetSelectionStrategy(); got != SelectionStrategyRoundRobin {
		t.Fatalf("GetSelectionStrategy: got %s want %s", got, SelectionStrategyRoundRobin)
	}
}

func TestConfigUpdater_SetSelectionStrategy_Invalid(t *testing.T) {
	m := newTestMixedProviders(t, ProviderChainTxBroadcasters)
	if err := m.SetSelectionStrategy(ProviderChainTxBroadcasters, SelectionStrategy("nonsense")); err == nil {
		t.Fatal("expected error for invalid strategy")
	}
}

func TestConfigUpdater_SetScoringWeights(t *testing.T) {
	m := newTestMixedProviders(t, ProviderChainBalanceFetchers)
	// Inject a scoring engine. We don't need its background goroutine running
	// for UpdateConfig; we just need a live *scoring.Engine.
	eng := scoring.NewEngine()
	defer eng.Stop()
	m.scoringEngine = eng

	w := ScoringWeights{
		HealthCheckFailPenalty:         7.0,
		RateLimitPenalty:               25.0,
		AuthFailurePenalty:             60.0,
		SlowResponsePenalty:            3.0,
		OperationFailPenalty:           4.0,
		TimeoutPenalty:                 12.0,
		SuccessBonus:                   0.7,
		MaxPenalty:                     95.0,
		DecayInterval:                  90 * time.Second,
		DecayRate:                      0.15,
		LatencyWindowSize:              200,
		SlowThresholdStdDev:            1.5,
		MinSamplesForLatencyComparison: 20,
	}
	if err := m.SetScoringWeights(w); err != nil {
		t.Fatalf("SetScoringWeights: %v", err)
	}

	got := eng.GetConfig()
	if got.HealthCheckFailPenalty != 7.0 || got.RateLimitPenalty != 25.0 || got.LatencyWindowSize != 200 {
		t.Fatalf("scoring config not applied: %+v", got)
	}
}

func TestConfigUpdater_SetScoringWeights_NoEngineIsNoOp(t *testing.T) {
	m := newTestMixedProviders(t)
	if err := m.SetScoringWeights(ScoringWeights{}); err != nil {
		t.Fatalf("SetScoringWeights without engine should be no-op: %v", err)
	}
}

func TestConfigUpdater_Snapshot(t *testing.T) {
	m := newTestMixedProviders(t, ProviderChainBalanceFetchers, ProviderChainTxBroadcasters)
	eng := scoring.NewEngine(scoring.WithRateLimitPenalty(33.0))
	defer eng.Stop()
	m.scoringEngine = eng

	snap := m.Snapshot()
	if _, ok := snap.Chains[ProviderChainBalanceFetchers]; !ok {
		t.Fatal("Snapshot missing BalanceFetchers")
	}
	if _, ok := snap.Chains[ProviderChainTxBroadcasters]; !ok {
		t.Fatal("Snapshot missing TxBroadcasters")
	}
	if _, ok := snap.Chains[ProviderChainAddressGenerators]; ok {
		t.Fatal("Snapshot included unconfigured AddressGenerators chain")
	}
	if !snap.ScoringEnabled {
		t.Fatal("ScoringEnabled should be true (engine default)")
	}
	if snap.ScoringWeights.RateLimitPenalty != 33.0 {
		t.Fatalf("ScoringWeights.RateLimitPenalty: got %v want 33", snap.ScoringWeights.RateLimitPenalty)
	}
}

func TestConfigUpdater_SnapshotIsACopy(t *testing.T) {
	// Mutating the snapshot map must not affect the live runtime config.
	m := newTestMixedProviders(t, ProviderChainBalanceFetchers)
	snap := m.Snapshot()
	cfg := snap.Chains[ProviderChainBalanceFetchers]
	cfg.Timeout = 99 * time.Hour
	snap.Chains[ProviderChainBalanceFetchers] = cfg

	live := m.balanceFetchers.config.Timeout
	if live == 99*time.Hour {
		t.Fatal("Snapshot returned a live reference; mutation leaked into runtime config")
	}
}
