// Package chainkit provides blockchain interaction capabilities
package chainkit

import (
	"context"

	"github.com/exapsy/chainkit/scoring"
)

// MixedProvidersBuilder implements the builder pattern for creating MixedProviders
type MixedProvidersBuilder struct {
	addressGenerators *providerManager
	addressValidators *providerManager
	feeEstimators     *providerManager
	feeRecommenders   *providerManager
	txBroadcasters    *providerManager
	utxoFetchers      *providerManager
	txAssemblers      *providerManager
	txSizers          *providerManager
	txSigners         *providerManager
	txStatusFetchers  *providerManager
	balanceFetchers   *providerManager
	rateFetchers      *providerManager

	chainConfigs    map[string]ChainConfig
	metricsRecorder MetricsRecorder
	scoringEngine   *scoring.Engine
}

// NewMixedProvidersBuilder creates a new builder with default configurations
func NewMixedProvidersBuilder() *MixedProvidersBuilder {
	return &MixedProvidersBuilder{
		addressGenerators: newProviderManager(DefaultChainConfig(ProviderChainAddressGenerators)),
		addressValidators: newProviderManager(DefaultChainConfig(ProviderChainAddressValidators)),
		feeEstimators:     newProviderManager(DefaultChainConfig(ProviderChainFeeEstimators)),
		feeRecommenders:   newProviderManager(DefaultChainConfig(ProviderChainFeeRecommenders)),
		txBroadcasters:    newProviderManager(DefaultChainConfig(ProviderChainTxBroadcasters)),
		utxoFetchers:      newProviderManager(DefaultChainConfig(ProviderChainUTXOFetchers)),
		txAssemblers:      newProviderManager(DefaultChainConfig(ProviderChainTxAssemblers)),
		txSizers:          newProviderManager(DefaultChainConfig(ProviderChainTxSizers)),
		txSigners:         newProviderManager(DefaultChainConfig(ProviderChainTxSigners)),
		txStatusFetchers:  newProviderManager(DefaultChainConfig(ProviderChainTxStatusFetchers)),
		balanceFetchers:   newProviderManager(DefaultChainConfig(ProviderChainBalanceFetchers)),
		rateFetchers:      newProviderManager(DefaultChainConfig(ProviderChainRateFetchers)),
		chainConfigs:      make(map[string]ChainConfig),
		metricsRecorder:   &NoOpMetricsRecorder{}, // Default to no-op metrics
	}
}

// WithChainConfig sets the configuration for a specific provider chain
func (b *MixedProvidersBuilder) WithChainConfig(chainType ProviderChainType, config ChainConfig) *MixedProvidersBuilder {
	chainName := chainType.String()
	b.chainConfigs[chainName] = config

	// Apply configuration to the appropriate provider manager
	switch chainType {
	case ProviderChainAddressGenerators:
		b.addressGenerators.updateChainConfig(config)
	case ProviderChainAddressValidators:
		b.addressValidators.updateChainConfig(config)
	case ProviderChainFeeEstimators:
		b.feeEstimators.updateChainConfig(config)
	case ProviderChainFeeRecommenders:
		b.feeRecommenders.updateChainConfig(config)
	case ProviderChainTxBroadcasters:
		b.txBroadcasters.updateChainConfig(config)
	case ProviderChainUTXOFetchers:
		b.utxoFetchers.updateChainConfig(config)
	case ProviderChainTxAssemblers:
		b.txAssemblers.updateChainConfig(config)
	case ProviderChainTxSizers:
		b.txSizers.updateChainConfig(config)
	case ProviderChainTxSigners:
		b.txSigners.updateChainConfig(config)
	case ProviderChainTxStatusFetchers:
		b.txStatusFetchers.updateChainConfig(config)
	case ProviderChainBalanceFetchers:
		b.balanceFetchers.updateChainConfig(config)
	case ProviderChainRateFetchers:
		b.rateFetchers.updateChainConfig(config)
	}

	return b
}

// WithMetricsRecorder sets the metrics recorder for tracking provider performance
func (b *MixedProvidersBuilder) WithMetricsRecorder(recorder MetricsRecorder) *MixedProvidersBuilder {
	if recorder == nil {
		b.metricsRecorder = &NoOpMetricsRecorder{}
	} else {
		b.metricsRecorder = recorder
	}
	return b
}

// WithAdaptiveScoring enables adaptive provider scoring with dynamic priority adjustment.
// The scoring engine automatically adjusts provider priorities based on:
//   - Health check results (429, auth failures, timeouts)
//   - Operation success/failure rates
//   - Response time relative to other providers
//
// Options can be passed to customize scoring behavior:
//
//	client := chainkit.NewMixedProvidersBuilder().
//	    WithTxBroadcasterChain(...).
//	    WithAdaptiveScoring(
//	        scoring.WithRateLimitPenalty(30.0),  // Higher penalty for 429s
//	        scoring.WithDecayRate(0.2),          // Faster recovery
//	    ).
//	    Build()
//
// When adaptive scoring is enabled, the initial priority values still matter as they
// determine the base score (priority 1 = 100 points, priority 2 = 90 points, etc.),
// but actual provider selection will be dynamically adjusted based on performance.
func (b *MixedProvidersBuilder) WithAdaptiveScoring(opts ...scoring.ScoringOption) *MixedProvidersBuilder {
	b.scoringEngine = scoring.NewEngine(opts...)
	return b
}

// WithAdaptiveScoringEngine sets a pre-configured scoring engine.
// Use this when you need to share a scoring engine across multiple builders
// or when you need more control over engine lifecycle.
func (b *MixedProvidersBuilder) WithAdaptiveScoringEngine(engine *scoring.Engine) *MixedProvidersBuilder {
	b.scoringEngine = engine
	return b
}

func (b *MixedProvidersBuilder) WithBalanceFetcherChain(fetchers ...BalanceFetcherConfig) *MixedProvidersBuilder {
	chainConfigApplied := false
	for _, fetcher := range fetchers {
		if fetcher.Fetcher == nil {
			continue
		}

		name := fetcher.Name
		if name == "" {
			if named, ok := fetcher.Fetcher.(BlockchainBaseProvider); ok {
				name = named.Name()
			} else {
				name = "unknown"
			}
		}

		if fetcher.ChainConfig != nil && !chainConfigApplied {
			b.balanceFetchers.updateChainConfig(*fetcher.ChainConfig)
			chainConfigApplied = true
		}

		b.balanceFetchers.addProvider(fetcher.Fetcher, fetcher.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithRateFetcherChain(fetchers ...RateFetcherConfig) *MixedProvidersBuilder {
	chainConfigApplied := false
	for _, fetcher := range fetchers {
		if fetcher.Fetcher == nil {
			continue
		}

		name := fetcher.Name
		if name == "" {
			if named, ok := fetcher.Fetcher.(BlockchainBaseProvider); ok {
				name = named.Name()
			} else {
				name = "unknown"
			}
		}

		if fetcher.ChainConfig != nil && !chainConfigApplied {
			b.rateFetchers.updateChainConfig(*fetcher.ChainConfig)
			chainConfigApplied = true
		}

		b.rateFetchers.addProvider(fetcher.Fetcher, fetcher.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithAddressGeneratorChain(generators ...AddressGeneratorConfig) *MixedProvidersBuilder {
	chainConfigApplied := false
	for _, generator := range generators {
		if generator.Generator == nil {
			continue
		}

		name := generator.Name
		if name == "" {
			if named, ok := generator.Generator.(BlockchainBaseProvider); ok {
				name = named.Name()
			} else {
				name = "unknown"
			}
		}

		if generator.ChainConfig != nil && !chainConfigApplied {
			b.addressGenerators.updateChainConfig(*generator.ChainConfig)
			chainConfigApplied = true
		}

		b.addressGenerators.addProvider(generator.Generator, generator.Priority, name)
	}
	return b
}

// WithFeeRecommenderChain adds one or more [FeeRecommender] providers with per-entry
// priority and optional chain configuration.
func (b *MixedProvidersBuilder) WithFeeRecommenderChain(recommenders ...FeeRecommenderConfig) *MixedProvidersBuilder {
	chainConfigApplied := false
	for _, r := range recommenders {
		if r.Recommender == nil {
			continue
		}

		name := r.Name
		if name == "" {
			if named, ok := r.Recommender.(BlockchainBaseProvider); ok {
				name = named.Name()
			} else {
				name = "unknown"
			}
		}

		if r.ChainConfig != nil && !chainConfigApplied {
			b.feeRecommenders.updateChainConfig(*r.ChainConfig)
			chainConfigApplied = true
		}

		b.feeRecommenders.addProvider(r.Recommender, r.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithFeeEstimatorChain(estimators ...FeeEstimatorConfig) *MixedProvidersBuilder {
	chainConfigApplied := false
	for _, estimator := range estimators {
		if estimator.Estimator == nil {
			continue
		}

		name := estimator.Name
		if name == "" {
			if named, ok := estimator.Estimator.(BlockchainBaseProvider); ok {
				name = named.Name()
			} else {
				name = "unknown"
			}
		}

		if estimator.ChainConfig != nil && !chainConfigApplied {
			b.feeEstimators.updateChainConfig(*estimator.ChainConfig)
			chainConfigApplied = true
		}

		b.feeEstimators.addProvider(estimator.Estimator, estimator.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithTxBroadcasterChain(broadcasters ...TxBroadcasterConfig) *MixedProvidersBuilder {
	chainConfigApplied := false
	for _, broadcaster := range broadcasters {
		if broadcaster.Broadcaster == nil {
			continue
		}

		name := broadcaster.Name
		if name == "" {
			if named, ok := broadcaster.Broadcaster.(BlockchainBaseProvider); ok {
				name = named.Name()
			} else {
				name = "unknown"
			}
		}

		if broadcaster.ChainConfig != nil && !chainConfigApplied {
			b.txBroadcasters.updateChainConfig(*broadcaster.ChainConfig)
			chainConfigApplied = true
		}

		b.txBroadcasters.addProvider(broadcaster.Broadcaster, broadcaster.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithTxAssemblerChain(assemblers ...TxAssemblerConfig) *MixedProvidersBuilder {
	chainConfigApplied := false
	for _, assembler := range assemblers {
		if assembler.Assembler == nil {
			continue
		}

		name := assembler.Name
		if name == "" {
			if named, ok := assembler.Assembler.(BlockchainBaseProvider); ok {
				name = named.Name()
			} else {
				name = "unknown"
			}
		}

		if assembler.ChainConfig != nil && !chainConfigApplied {
			b.txAssemblers.updateChainConfig(*assembler.ChainConfig)
			chainConfigApplied = true
		}

		b.txAssemblers.addProvider(assembler.Assembler, assembler.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithTxSizerChain(sizers ...TxSizerConfig) *MixedProvidersBuilder {
	chainConfigApplied := false
	for _, sizer := range sizers {
		if sizer.Sizer == nil {
			continue
		}

		name := sizer.Name
		if name == "" {
			if named, ok := sizer.Sizer.(BlockchainBaseProvider); ok {
				name = named.Name()
			} else {
				name = "unknown"
			}
		}

		if sizer.ChainConfig != nil && !chainConfigApplied {
			b.txSizers.updateChainConfig(*sizer.ChainConfig)
			chainConfigApplied = true
		}

		b.txSizers.addProvider(sizer.Sizer, sizer.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithTxSignerChain(signers ...TxSignerConfig) *MixedProvidersBuilder {
	chainConfigApplied := false
	for _, signer := range signers {
		if signer.Signer == nil {
			continue
		}

		name := signer.Name
		if name == "" {
			if named, ok := signer.Signer.(BlockchainBaseProvider); ok {
				name = named.Name()
			} else {
				name = "unknown"
			}
		}

		if signer.ChainConfig != nil && !chainConfigApplied {
			b.txSigners.updateChainConfig(*signer.ChainConfig)
			chainConfigApplied = true
		}

		b.txSigners.addProvider(signer.Signer, signer.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithUTXOFetcherChain(fetchers ...UTXOFetcherConfig) *MixedProvidersBuilder {
	chainConfigApplied := false
	for _, fetcher := range fetchers {
		if fetcher.Fetcher == nil {
			continue
		}

		name := fetcher.Name
		if name == "" {
			if named, ok := fetcher.Fetcher.(BlockchainBaseProvider); ok {
				name = named.Name()
			} else {
				name = "unknown"
			}
		}

		if fetcher.ChainConfig != nil && !chainConfigApplied {
			b.utxoFetchers.updateChainConfig(*fetcher.ChainConfig)
			chainConfigApplied = true
		}

		b.utxoFetchers.addProvider(fetcher.Fetcher, fetcher.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithAddressValidatorChain(validators ...AddressValidatorConfig) *MixedProvidersBuilder {
	chainConfigApplied := false
	for _, validator := range validators {
		if validator.Validator == nil {
			continue
		}

		name := validator.Name
		if name == "" {
			if named, ok := validator.Validator.(BlockchainBaseProvider); ok {
				name = named.Name()
			} else {
				name = "unknown"
			}
		}

		if validator.ChainConfig != nil && !chainConfigApplied {
			b.addressValidators.updateChainConfig(*validator.ChainConfig)
			chainConfigApplied = true
		}

		b.addressValidators.addProvider(validator.Validator, validator.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithTxStatusFetcherChain(fetchers ...TxStatusFetcherConfig) *MixedProvidersBuilder {
	chainConfigApplied := false
	for _, fetcher := range fetchers {
		if fetcher.Fetcher == nil {
			continue
		}

		name := fetcher.Name
		if name == "" {
			if named, ok := fetcher.Fetcher.(BlockchainBaseProvider); ok {
				name = named.Name()
			} else {
				name = "unknown"
			}
		}

		if fetcher.ChainConfig != nil && !chainConfigApplied {
			b.txStatusFetchers.updateChainConfig(*fetcher.ChainConfig)
			chainConfigApplied = true
		}

		b.txStatusFetchers.addProvider(fetcher.Fetcher, fetcher.Priority, name)
	}
	return b
}

// Build creates the final [MixedProviders] instance.
//
// Not all roles need to be registered. If you call a method whose role has no
// registered provider you will receive an [ErrProviderNotConfigured] error at
// that point — no upfront validation is performed.
//
// If adaptive scoring was enabled via [WithAdaptiveScoring], the scoring engine
// will be started automatically. Call [MixedProviders.StopScoring] when done to
// clean up background goroutines.
func (b *MixedProvidersBuilder) Build() BlockchainProvider {
	// Apply scoring engine to all provider managers if enabled
	if b.scoringEngine != nil {
		managers := []*providerManager{
			b.addressGenerators,
			b.addressValidators,
			b.feeRecommenders,
			b.feeEstimators,
			b.txBroadcasters,
			b.txAssemblers,
			b.txSizers,
			b.txSigners,
			b.txStatusFetchers,
			b.utxoFetchers,
			b.balanceFetchers,
			b.rateFetchers,
		}

		for _, manager := range managers {
			if manager != nil {
				manager.SetScoringEngine(b.scoringEngine)
			}
		}

		// Start the scoring engine with a background context
		// Users can call StopScoring() to clean up
		b.scoringEngine.Start(context.Background())
	}

	return &MixedProviders{
		addressGenerators: b.addressGenerators,
		addressValidators: b.addressValidators,
		feeRecommenders:   b.feeRecommenders,
		feeEstimators:     b.feeEstimators,
		txBroadcasters:    b.txBroadcasters,
		txAssemblers:      b.txAssemblers,
		txSizers:          b.txSizers,
		txSigners:         b.txSigners,
		txStatusFetchers:  b.txStatusFetchers,
		utxoFetchers:      b.utxoFetchers,
		balanceFetchers:   b.balanceFetchers,
		rateFetchers:      b.rateFetchers,
		metricsRecorder:   b.metricsRecorder,
		scoringEngine:     b.scoringEngine,
	}
}
