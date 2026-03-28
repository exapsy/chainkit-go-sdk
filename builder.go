// Package chainkit provides blockchain interaction capabilities
package chainkit

import "fmt"

// MixedProvidersBuilder implements the builder pattern for creating MixedProviders
type MixedProvidersBuilder struct {
	addressGenerators *ProviderManager
	addressValidators *ProviderManager
	txMonitors        *ProviderManager
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

func (b *MixedProvidersBuilder) AddAddressGenerator(generator AddressGenerator, priority int, name string) *MixedProvidersBuilder {
	b.addressGenerators.AddProvider(generator, priority, name)
	return b
}

func (b *MixedProvidersBuilder) AddFeeRecommender(recommender FeeFetcher, priority int, name string) *MixedProvidersBuilder {
	b.feeRecommenders.AddProvider(recommender, priority, name)
	return b
}

func (b *MixedProvidersBuilder) AddFeeEstimator(estimator FeeEstimator, priority int, name string) *MixedProvidersBuilder {
	b.feeEstimators.AddProvider(estimator, priority, name)
	return b
}

func (b *MixedProvidersBuilder) AddTxBroadcaster(broadcaster TxBroadcaster, priority int, name string) *MixedProvidersBuilder {
	b.txBroadcasters.AddProvider(broadcaster, priority, name)
	return b
}

func (b *MixedProvidersBuilder) AddTxAssembler(assembler TxAssembler, priority int, name string) *MixedProvidersBuilder {
	b.txAssemblers.AddProvider(assembler, priority, name)
	return b
}

func (b *MixedProvidersBuilder) AddTxSizer(sizer TxSizer, priority int, name string) *MixedProvidersBuilder {
	b.txSizers.AddProvider(sizer, priority, name)
	return b
}

func (b *MixedProvidersBuilder) AddTxSigner(signer TxSigner, priority int, name string) *MixedProvidersBuilder {
	b.txSigners.AddProvider(signer, priority, name)
	return b
}

func (b *MixedProvidersBuilder) AddUTXOFetcher(fetcher UTXOFetcher, priority int, name string) *MixedProvidersBuilder {
	b.utxoFetchers.AddProvider(fetcher, priority, name)
	return b
}

func (b *MixedProvidersBuilder) AddBalanceFetcher(fetcher BalanceFetcher, priority int, name string) *MixedProvidersBuilder {
	b.balanceFetchers.AddProvider(fetcher, priority, name)
	return b
}

func (b *MixedProvidersBuilder) AddRateFetcher(fetcher RateFetcher, priority int, name string) *MixedProvidersBuilder {
	b.rateFetchers.AddProvider(fetcher, priority, name)
	return b
}

func (b *MixedProvidersBuilder) AddTxStatusFetcher(fetcher TxStatusFetcher, priority int, name string) *MixedProvidersBuilder {
	b.txStatusFetchers.AddProvider(fetcher, priority, name)
	return b
}

func (b *MixedProvidersBuilder) AddAddressValidator(validator AddressValidator, priority int, name string) *MixedProvidersBuilder {
	b.addressValidators.AddProvider(validator, priority, name)
	return b
}

// WithBalanceFetcherChain adds a chain of BalanceFetchers to the builder
func (b *MixedProvidersBuilder) WithBalanceFetcherChain(fetchers ...BalanceFetcherConfig) *MixedProvidersBuilder {
	for _, fetcher := range fetchers {
		// Skip nil fetchers to prevent panics
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

		if fetcher.ChainConfig != nil {
			b.balanceFetchers.UpdateChainConfig(*fetcher.ChainConfig)
		}

		b.balanceFetchers.AddProvider(fetcher.Fetcher, fetcher.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithRateFetcherChain(fetchers ...RateFetcherConfig) *MixedProvidersBuilder {
	for _, fetcher := range fetchers {
		// Skip nil fetchers to prevent panics
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

		if fetcher.ChainConfig != nil {
			b.rateFetchers.UpdateChainConfig(*fetcher.ChainConfig)
		}

		b.rateFetchers.AddProvider(fetcher.Fetcher, fetcher.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithAddressGeneratorChain(generators ...AddressGeneratorConfig) *MixedProvidersBuilder {
	for _, generator := range generators {
		// Skip nil generators to prevent panics
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

		if generator.ChainConfig != nil {
			b.addressGenerators.UpdateChainConfig(*generator.ChainConfig)
		}

		b.addressGenerators.AddProvider(generator.Generator, generator.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithFeeFetcherChain(fetchers ...FeeFetcherConfig) *MixedProvidersBuilder {
	for _, fetcher := range fetchers {
		// Skip nil fetchers to prevent panics
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

		if fetcher.ChainConfig != nil {
			b.feeRecommenders.UpdateChainConfig(*fetcher.ChainConfig)
		}

		b.feeRecommenders.AddProvider(fetcher.Fetcher, fetcher.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithFeeEstimatorChain(estimators ...FeeEstimatorConfig) *MixedProvidersBuilder {
	for _, estimator := range estimators {
		// Skip nil estimators to prevent panics
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

		if estimator.ChainConfig != nil {
			b.feeEstimators.UpdateChainConfig(*estimator.ChainConfig)
		}

		b.feeEstimators.AddProvider(estimator.Estimator, estimator.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithTxBroadcasterChain(broadcasters ...TxBroadcasterConfig) *MixedProvidersBuilder {
	for _, broadcaster := range broadcasters {
		// Skip nil broadcasters to prevent panics
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

		if broadcaster.ChainConfig != nil {
			b.txBroadcasters.UpdateChainConfig(*broadcaster.ChainConfig)
		}

		b.txBroadcasters.AddProvider(broadcaster.Broadcaster, broadcaster.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithTxAssemblerChain(assemblers ...TxAssemblerConfig) *MixedProvidersBuilder {
	for _, assembler := range assemblers {
		// Skip nil assemblers to prevent panics
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

		if assembler.ChainConfig != nil {
			b.txAssemblers.UpdateChainConfig(*assembler.ChainConfig)
		}

		b.txAssemblers.AddProvider(assembler.Assembler, assembler.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithTxSizerChain(sizers ...TxSizerConfig) *MixedProvidersBuilder {
	for _, sizer := range sizers {
		// Skip nil sizers to prevent panics
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

		if sizer.ChainConfig != nil {
			b.txSizers.UpdateChainConfig(*sizer.ChainConfig)
		}

		b.txSizers.AddProvider(sizer.Sizer, sizer.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithTxSignerChain(signers ...TxSignerConfig) *MixedProvidersBuilder {
	for _, signer := range signers {
		// Skip nil signers to prevent panics
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

		if signer.ChainConfig != nil {
			b.txSigners.UpdateChainConfig(*signer.ChainConfig)
		}

		b.txSigners.AddProvider(signer.Signer, signer.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithUTXOFetcherChain(fetchers ...UTXOFetcherConfig) *MixedProvidersBuilder {
	for _, fetcher := range fetchers {
		// Skip nil fetchers to prevent panics
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

		if fetcher.ChainConfig != nil {
			b.utxoFetchers.UpdateChainConfig(*fetcher.ChainConfig)
		}

		b.utxoFetchers.AddProvider(fetcher.Fetcher, fetcher.Priority, name)
	}
	return b
}

func (b *MixedProvidersBuilder) WithAddressValidatorChain(validators ...AddressValidatorConfig) *MixedProvidersBuilder {
	for _, validator := range validators {
		// Skip nil validators to prevent panics
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

		if validator.ChainConfig != nil {
			b.addressValidators.UpdateChainConfig(*validator.ChainConfig)
		}

		b.addressValidators.AddProvider(validator.Validator, validator.Priority, name)
	}
	return b
}

// WithAddressGenerator is a Legacy single-provider methods for backward compatibility
func (b *MixedProvidersBuilder) WithAddressGenerator(generator AddressGenerator) *MixedProvidersBuilder {
	return b.AddAddressGenerator(generator, 1, "default")
}

func (b *MixedProvidersBuilder) WithFeeRecommender(recommender FeeFetcher) *MixedProvidersBuilder {
	return b.AddFeeRecommender(recommender, 1, "default")
}

func (b *MixedProvidersBuilder) WithFeeEstimator(estimator FeeEstimator) *MixedProvidersBuilder {
	return b.AddFeeEstimator(estimator, 1, "default")
}

func (b *MixedProvidersBuilder) WithTxBroadcaster(broadcaster TxBroadcaster) *MixedProvidersBuilder {
	return b.AddTxBroadcaster(broadcaster, 1, "default")
}

func (b *MixedProvidersBuilder) WithTxAssembler(assembler TxAssembler) *MixedProvidersBuilder {
	return b.AddTxAssembler(assembler, 1, "default")
}

func (b *MixedProvidersBuilder) WithTxSizer(sizer TxSizer) *MixedProvidersBuilder {
	return b.AddTxSizer(sizer, 1, "default")
}

func (b *MixedProvidersBuilder) WithTxSigner(signer TxSigner) *MixedProvidersBuilder {
	return b.AddTxSigner(signer, 1, "default")
}

func (b *MixedProvidersBuilder) WithUTXOFetcher(fetcher UTXOFetcher) *MixedProvidersBuilder {
	return b.AddUTXOFetcher(fetcher, 1, "default")
}

func (b *MixedProvidersBuilder) WithBalanceFetcher(fetcher BalanceFetcher) *MixedProvidersBuilder {
	return b.AddBalanceFetcher(fetcher, 1, "default")
}

func (b *MixedProvidersBuilder) WithRateFetcher(fetcher RateFetcher) *MixedProvidersBuilder {
	return b.AddRateFetcher(fetcher, 1, "default")
}

func (b *MixedProvidersBuilder) WithAddressValidator(validator AddressValidator) *MixedProvidersBuilder {
	return b.AddAddressValidator(validator, 1, "default")
}

func (b *MixedProvidersBuilder) WithTxStatusFetcher(fetcher TxStatusFetcher) *MixedProvidersBuilder {
	return b.AddTxStatusFetcher(fetcher, 1, "default")
}

// Build creates the final MixedProviders instance
func (b *MixedProvidersBuilder) Build() BlockchainProvider {
	// Check that at least one provider exists for each required interface
	managers := map[string]*ProviderManager{
		ProviderTypeAddressGenerator.String(): b.addressGenerators,
		ProviderTypeFeeRecommender.String():   b.feeRecommenders,
		ProviderTypeFeeEstimator.String():     b.feeEstimators,
		ProviderTypeTxBroadcaster.String():    b.txBroadcasters,
		ProviderTypeUTXOFetcher.String():      b.utxoFetchers,
		ProviderTypeTxAssembler.String():      b.txAssemblers,
		ProviderTypeTxSizer.String():          b.txSizers,
		ProviderTypeTxSigner.String():         b.txSigners,
		ProviderTypeBalanceFetcher.String():   b.balanceFetchers,
		ProviderTypeRateFetcher.String():      b.rateFetchers,
	}

	var missingInterfaces []string
	for name, manager := range managers {
		if len(manager.providers) == 0 {
			missingInterfaces = append(missingInterfaces, name)
		}
	}

	if len(missingInterfaces) > 0 {
		panic("Missing required interfaces: " + fmt.Sprintf("%v", missingInterfaces))
	}

	return &MixedProviders{
		addressGenerators: b.addressGenerators,
		addressValidators: b.addressValidators,
		txMonitors:        b.txMonitors,
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
