package chainkit

// ProviderChainType represents the type of a provider chain for type safety
type ProviderChainType string

// Provider chain type constants for type-safe provider identification
const (
	ProviderChainAddressGenerators ProviderChainType = "AddressGenerators"
	ProviderChainAddressValidators ProviderChainType = "AddressValidators"
	ProviderChainFeeEstimators     ProviderChainType = "FeeEstimators"   // FeeEstimator interface - calculates fee amounts
	ProviderChainFeeRecommenders   ProviderChainType = "FeeRecommenders" // FeeRecommender interface - fetches fee recommendations
	ProviderChainTxBroadcasters    ProviderChainType = "TxBroadcasters"
	ProviderChainUTXOFetchers      ProviderChainType = "UTXOFetchers"
	ProviderChainTxAssemblers      ProviderChainType = "TxAssemblers"
	ProviderChainTxSizers          ProviderChainType = "TxSizers"
	ProviderChainTxSigners         ProviderChainType = "TxSigners"
	ProviderChainTxStatusFetchers  ProviderChainType = "TxStatusFetchers"
	ProviderChainBalanceFetchers   ProviderChainType = "BalanceFetchers"
	ProviderChainRateFetchers      ProviderChainType = "RateFetchers"
)

// String returns the string representation of the provider chain type
func (p ProviderChainType) String() string {
	return string(p)
}

// ProviderType represents the type of a provider for type safety
type ProviderType string

// Provider type constants for type-safe provider identification
const (
	ProviderTypeAddressGenerator ProviderType = "addressGenerator"
	ProviderTypeAddressValidator ProviderType = "addressValidator"
	ProviderTypeFeeRecommender   ProviderType = "feeRecommender"
	ProviderTypeFeeEstimator     ProviderType = "feeEstimator"
	ProviderTypeTxBroadcaster    ProviderType = "txBroadcaster"
	ProviderTypeUTXOFetcher      ProviderType = "utxoFetcher"
	ProviderTypeTxAssembler      ProviderType = "txAssembler"
	ProviderTypeTxSizer          ProviderType = "txSizer"
	ProviderTypeTxSigner         ProviderType = "txSigner"
	ProviderTypeBalanceFetcher   ProviderType = "balanceFetcher"
	ProviderTypeRateFetcher      ProviderType = "rateFetcher"
)

// String returns the string representation of the provider type
func (p ProviderType) String() string {
	return string(p)
}

// SelectionStrategy represents the strategy used to select providers
type SelectionStrategy string

// Selection strategy constants
const (
	// SelectionStrategyPriorityOnly always selects providers in priority order (current behavior)
	SelectionStrategyPriorityOnly SelectionStrategy = "priority_only"

	// SelectionStrategyRoundRobin uses round-robin among providers with the same priority
	SelectionStrategyRoundRobin SelectionStrategy = "round_robin"

	// SelectionStrategyRandom randomly selects among providers with the same priority (future)
	SelectionStrategyRandom SelectionStrategy = "random"

	// SelectionStrategyLeastLoaded selects the least loaded provider (future)
	SelectionStrategyLeastLoaded SelectionStrategy = "least_loaded"
)

// String returns the string representation of the selection strategy
func (s SelectionStrategy) String() string {
	return string(s)
}

// IsValid checks if the selection strategy is valid
func (s SelectionStrategy) IsValid() bool {
	switch s {
	case SelectionStrategyPriorityOnly, SelectionStrategyRoundRobin, SelectionStrategyRandom, SelectionStrategyLeastLoaded:
		return true
	default:
		return false
	}
}
