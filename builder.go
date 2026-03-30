// Package chainkit provides blockchain interaction capabilities
package chainkit

// MixedProvidersBuilder implements the builder pattern for creating MixedProviders
type MixedProvidersBuilder struct {
	addressGenerators *ProviderManager
	addressValidators *ProviderManager
	feeEstimators     *ProviderManager
	feeRecommenders   *ProviderManager
	txBroadcasters    *ProviderManager
	utxoFetchers      *ProviderManager
	txAssemblers      *ProviderManager
	txSizers          *ProviderManager
	txSigners         *ProviderManager
	txStatusFetchers  *ProviderManager
	balanceFetchers   *ProviderManager
	rateFetchers      *ProviderManager

	chainConfigs    map[string]ChainConfig
	metricsRecorder MetricsRecorder
}

// NewMixedProvidersBuilder creates a new builder with default configurations
func NewMixedProvidersBuilder() *MixedProvidersBuilder {
	return &MixedProvidersBuilder{
		addressGenerators: NewProviderManager(DefaultChainConfig(ProviderChainAddressGenerators)),
		addressValidators: NewProviderManager(DefaultChainConfig(ProviderChainAddressValidators)),
		feeEstimators:     NewProviderManager(DefaultChainConfig(ProviderChainFeeEstimators)),
		feeRecommenders:   NewProviderManager(DefaultChainConfig(ProviderChainFeeRecommenders)),
		txBroadcasters:    NewProviderManager(DefaultChainConfig(ProviderChainTxBroadcasters)),
		utxoFetchers:      NewProviderManager(DefaultChainConfig(ProviderChainUTXOFetchers)),
		txAssemblers:      NewProviderManager(DefaultChainConfig(ProviderChainTxAssemblers)),
		txSizers:          NewProviderManager(DefaultChainConfig(ProviderChainTxSizers)),
		txSigners:         NewProviderManager(DefaultChainConfig(ProviderChainTxSigners)),
		txStatusFetchers:  NewProviderManager(DefaultChainConfig(ProviderChainTxStatusFetchers)),
		balanceFetchers:   NewProviderManager(DefaultChainConfig(ProviderChainBalanceFetchers)),
		rateFetchers:      NewProviderManager(DefaultChainConfig(ProviderChainRateFetchers)),
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
		b.addressGenerators.UpdateChainConfig(config)
	case ProviderChainAddressValidators:
		b.addressValidators.UpdateChainConfig(config)
	case ProviderChainFeeEstimators:
		b.feeEstimators.UpdateChainConfig(config)
	case ProviderChainFeeRecommenders:
		b.feeRecommenders.UpdateChainConfig(config)
	case ProviderChainTxBroadcasters:
		b.txBroadcasters.UpdateChainConfig(config)
	case ProviderChainUTXOFetchers:
		b.utxoFetchers.UpdateChainConfig(config)
	case ProviderChainTxAssemblers:
		b.txAssemblers.UpdateChainConfig(config)
	case ProviderChainTxSizers:
		b.txSizers.UpdateChainConfig(config)
	case ProviderChainTxSigners:
		b.txSigners.UpdateChainConfig(config)
	case ProviderChainTxStatusFetchers:
		b.txStatusFetchers.UpdateChainConfig(config)
	case ProviderChainBalanceFetchers:
		b.balanceFetchers.UpdateChainConfig(config)
	case ProviderChainRateFetchers:
		b.rateFetchers.UpdateChainConfig(config)
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
			b.balanceFetchers.UpdateChainConfig(*fetcher.ChainConfig)
			chainConfigApplied = true
		}

		b.balanceFetchers.AddProvider(fetcher.Fetcher, fetcher.Priority, name)
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
			b.rateFetchers.UpdateChainConfig(*fetcher.ChainConfig)
			chainConfigApplied = true
		}

		b.rateFetchers.AddProvider(fetcher.Fetcher, fetcher.Priority, name)
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
			b.addressGenerators.UpdateChainConfig(*generator.ChainConfig)
			chainConfigApplied = true
		}

		b.addressGenerators.AddProvider(generator.Generator, generator.Priority, name)
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
			b.feeRecommenders.UpdateChainConfig(*r.ChainConfig)
			chainConfigApplied = true
		}

		b.feeRecommenders.AddProvider(r.Recommender, r.Priority, name)
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
			b.feeEstimators.UpdateChainConfig(*estimator.ChainConfig)
			chainConfigApplied = true
		}

		b.feeEstimators.AddProvider(estimator.Estimator, estimator.Priority, name)
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
			b.txBroadcasters.UpdateChainConfig(*broadcaster.ChainConfig)
			chainConfigApplied = true
		}

		b.txBroadcasters.AddProvider(broadcaster.Broadcaster, broadcaster.Priority, name)
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
			b.txAssemblers.UpdateChainConfig(*assembler.ChainConfig)
			chainConfigApplied = true
		}

		b.txAssemblers.AddProvider(assembler.Assembler, assembler.Priority, name)
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
			b.txSizers.UpdateChainConfig(*sizer.ChainConfig)
			chainConfigApplied = true
		}

		b.txSizers.AddProvider(sizer.Sizer, sizer.Priority, name)
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
			b.txSigners.UpdateChainConfig(*signer.ChainConfig)
			chainConfigApplied = true
		}

		b.txSigners.AddProvider(signer.Signer, signer.Priority, name)
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
			b.utxoFetchers.UpdateChainConfig(*fetcher.ChainConfig)
			chainConfigApplied = true
		}

		b.utxoFetchers.AddProvider(fetcher.Fetcher, fetcher.Priority, name)
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
			b.addressValidators.UpdateChainConfig(*validator.ChainConfig)
			chainConfigApplied = true
		}

		b.addressValidators.AddProvider(validator.Validator, validator.Priority, name)
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
			b.txStatusFetchers.UpdateChainConfig(*fetcher.ChainConfig)
			chainConfigApplied = true
		}

		b.txStatusFetchers.AddProvider(fetcher.Fetcher, fetcher.Priority, name)
	}
	return b
}

// Build creates the final [MixedProviders] instance.
//
// Not all roles need to be registered. If you call a method whose role has no
// registered provider you will receive an [ErrProviderNotConfigured] error at
// that point — no upfront validation is performed.
func (b *MixedProvidersBuilder) Build() BlockchainProvider {
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
	}
}
