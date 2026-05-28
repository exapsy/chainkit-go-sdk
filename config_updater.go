package chainkit

import (
	"fmt"
	"time"

	"github.com/exapsy/chainkit/scoring"
)

// ConfigUpdater is the runtime configuration surface for *MixedProviders.
// Remote configuration agents (such as cloudagent) implement a polling loop that
// fetches a desired-state config blob from a control plane and calls these methods
// to bring the running SDK into alignment, without requiring a process restart.
//
// All methods are concurrency-safe and may be called from any goroutine while
// blockchain operations are in flight. Updates are atomic at the per-chain or
// per-engine granularity — an in-flight call uses either the old or new
// configuration end-to-end, never a half-applied mix.
//
// What ConfigUpdater can change:
//   - Per-chain ChainConfig (retry, circuit-breaker, rate-limit, health-check, timeout, max-concurrency)
//   - Per-chain SelectionStrategy
//   - Scoring engine penalty weights and decay settings
//   - Whether adaptive scoring is on or off
//
// What it deliberately cannot change (in v1):
//   - The set of registered providers per chain (providers carry credentials; runtime
//     mutation would require shipping secrets through the control plane and risks
//     half-configured chains under concurrency). Adding or removing providers
//     continues to require a process restart.
type ConfigUpdater interface {
	// UpdateChainConfig replaces the configuration of a single provider chain.
	// Returns an error if chain does not name a known ProviderChainType.
	UpdateChainConfig(chain ProviderChainType, cfg ChainConfig) error

	// SetSelectionStrategy switches the selection strategy for a single provider chain.
	// Returns an error if chain or strategy is invalid.
	SetSelectionStrategy(chain ProviderChainType, s SelectionStrategy) error

	// SetScoringWeights updates the scoring engine's penalty weights and decay
	// settings. Has no effect (returns nil) when adaptive scoring is not configured.
	SetScoringWeights(w ScoringWeights) error

	// SetScoringEnabled toggles adaptive scoring on or off at runtime. Has no effect
	// when adaptive scoring is not configured.
	SetScoringEnabled(enabled bool)

	// Snapshot returns a structural copy of the current runtime configuration.
	// Useful for diffing against a desired state, or for "what config am I running"
	// reporting in dashboards.
	Snapshot() ConfigSnapshot
}

// ScoringWeights is the JSON-serialisable subset of scoring.ScoringConfig that a
// remote control plane is allowed to mutate. Storage backends, metrics recorders,
// and other process-local concerns are deliberately excluded — they are wired at
// build time only.
type ScoringWeights struct {
	HealthCheckFailPenalty         float64       `json:"health_check_fail_penalty"`
	RateLimitPenalty               float64       `json:"rate_limit_penalty"`
	AuthFailurePenalty             float64       `json:"auth_failure_penalty"`
	SlowResponsePenalty            float64       `json:"slow_response_penalty"`
	OperationFailPenalty           float64       `json:"operation_fail_penalty"`
	TimeoutPenalty                 float64       `json:"timeout_penalty"`
	SuccessBonus                   float64       `json:"success_bonus"`
	MaxPenalty                     float64       `json:"max_penalty"`
	DecayInterval                  time.Duration `json:"decay_interval"`
	DecayRate                      float64       `json:"decay_rate"`
	LatencyWindowSize              int           `json:"latency_window_size"`
	SlowThresholdStdDev            float64       `json:"slow_threshold_std_dev"`
	MinSamplesForLatencyComparison int           `json:"min_samples_for_latency_comparison"`
}

// ConfigSnapshot is a structural copy of a *MixedProviders' current runtime
// configuration. The map is keyed by ProviderChainType; chains that have no
// registered providers are still present (they hold the initial DefaultChainConfig).
type ConfigSnapshot struct {
	Chains         map[ProviderChainType]ChainConfig `json:"chains"`
	ScoringEnabled bool                              `json:"scoring_enabled"`
	ScoringWeights ScoringWeights                    `json:"scoring_weights"`
}

// scoringConfigToWeights extracts the JSON-safe subset of a scoring.ScoringConfig.
func scoringConfigToWeights(c scoring.ScoringConfig) ScoringWeights {
	return ScoringWeights{
		HealthCheckFailPenalty:         c.HealthCheckFailPenalty,
		RateLimitPenalty:               c.RateLimitPenalty,
		AuthFailurePenalty:             c.AuthFailurePenalty,
		SlowResponsePenalty:            c.SlowResponsePenalty,
		OperationFailPenalty:           c.OperationFailPenalty,
		TimeoutPenalty:                 c.TimeoutPenalty,
		SuccessBonus:                   c.SuccessBonus,
		MaxPenalty:                     c.MaxPenalty,
		DecayInterval:                  c.DecayInterval,
		DecayRate:                      c.DecayRate,
		LatencyWindowSize:              c.LatencyWindowSize,
		SlowThresholdStdDev:            c.SlowThresholdStdDev,
		MinSamplesForLatencyComparison: c.MinSamplesForLatencyComparison,
	}
}

// scoringWeightsToOptions converts ScoringWeights into the variadic functional-option
// slice that scoring.Engine.UpdateConfig consumes. Every field is applied unconditionally
// — callers that want a partial update should fetch the current snapshot, merge their
// overrides, and pass the merged value back.
func scoringWeightsToOptions(w ScoringWeights) []scoring.ScoringOption {
	return []scoring.ScoringOption{
		scoring.WithHealthCheckPenalty(w.HealthCheckFailPenalty),
		scoring.WithRateLimitPenalty(w.RateLimitPenalty),
		scoring.WithAuthFailurePenalty(w.AuthFailurePenalty),
		scoring.WithSlowResponsePenalty(w.SlowResponsePenalty),
		scoring.WithOperationFailPenalty(w.OperationFailPenalty),
		scoring.WithTimeoutPenalty(w.TimeoutPenalty),
		scoring.WithSuccessBonus(w.SuccessBonus),
		scoring.WithMaxPenalty(w.MaxPenalty),
		scoring.WithDecayInterval(w.DecayInterval),
		scoring.WithDecayRate(w.DecayRate),
		scoring.WithLatencyWindow(w.LatencyWindowSize),
		scoring.WithSlowThreshold(w.SlowThresholdStdDev),
		scoring.WithMinLatencySamples(w.MinSamplesForLatencyComparison),
	}
}

// managers returns every providerManager attached to this MixedProviders, keyed
// by its ProviderChainType. Nil entries are skipped — a chain with no registered
// providers has no manager to address.
func (m *MixedProviders) managers() map[ProviderChainType]*providerManager {
	return map[ProviderChainType]*providerManager{
		ProviderChainAddressGenerators: m.addressGenerators,
		ProviderChainAddressValidators: m.addressValidators,
		ProviderChainFeeEstimators:     m.feeEstimators,
		ProviderChainFeeRecommenders:   m.feeRecommenders,
		ProviderChainTxBroadcasters:    m.txBroadcasters,
		ProviderChainUTXOFetchers:      m.utxoFetchers,
		ProviderChainTxAssemblers:      m.txAssemblers,
		ProviderChainTxSizers:          m.txSizers,
		ProviderChainTxSigners:         m.txSigners,
		ProviderChainTxStatusFetchers:  m.txStatusFetchers,
		ProviderChainBalanceFetchers:   m.balanceFetchers,
		ProviderChainRateFetchers:      m.rateFetchers,
		ProviderChainHistoricalRateFetchers: m.historicalRateFetchers,
	}
}

// managerFor resolves a chain type to its providerManager.
// Returns ErrProviderNotConfigured wrapped with the chain name when the chain has
// no registered providers, and a distinct error when the chain name itself is not
// a known ProviderChainType.
func (m *MixedProviders) managerFor(chain ProviderChainType) (*providerManager, error) {
	all := m.managers()
	pm, known := all[chain]
	if !known {
		return nil, fmt.Errorf("unknown provider chain: %q", chain)
	}
	if pm == nil {
		return nil, fmt.Errorf("%w: chain %s has no providers registered", ErrProviderNotConfigured, chain)
	}
	return pm, nil
}

// UpdateChainConfig replaces the configuration of a single provider chain.
func (m *MixedProviders) UpdateChainConfig(chain ProviderChainType, cfg ChainConfig) error {
	pm, err := m.managerFor(chain)
	if err != nil {
		return err
	}
	pm.updateChainConfig(cfg)
	return nil
}

// SetSelectionStrategy switches the selection strategy for a single provider chain.
func (m *MixedProviders) SetSelectionStrategy(chain ProviderChainType, s SelectionStrategy) error {
	pm, err := m.managerFor(chain)
	if err != nil {
		return err
	}
	return pm.SetSelectionStrategy(s)
}

// SetScoringWeights forwards the weights to the underlying scoring engine.
// No-op (returns nil) when adaptive scoring was never configured.
func (m *MixedProviders) SetScoringWeights(w ScoringWeights) error {
	if m.scoringEngine == nil {
		return nil
	}
	return m.scoringEngine.UpdateConfig(scoringWeightsToOptions(w)...)
}

// Snapshot returns a structural copy of the current runtime configuration.
func (m *MixedProviders) Snapshot() ConfigSnapshot {
	chainsCopy := make(map[ProviderChainType]ChainConfig, 12)
	for chain, pm := range m.managers() {
		if pm == nil {
			continue
		}
		pm.mutex.RLock()
		chainsCopy[chain] = pm.config
		pm.mutex.RUnlock()
	}

	snap := ConfigSnapshot{Chains: chainsCopy}
	if m.scoringEngine != nil {
		snap.ScoringEnabled = m.scoringEngine.IsEnabled()
		snap.ScoringWeights = scoringConfigToWeights(m.scoringEngine.GetConfig())
	}
	return snap
}

// Compile-time assertion that *MixedProviders satisfies ConfigUpdater.
var _ ConfigUpdater = (*MixedProviders)(nil)
