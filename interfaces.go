// Package chainkit provides a chain-agnostic multi-provider blockchain client with
// automatic fallback, circuit breaking, and metrics support.
//
// # Getting started
//
// Build a [BlockchainProvider] using [NewMixedProvidersBuilder], register providers
// from the bitcoin/providers sub-package, then call [MixedProvidersBuilder.Build]:
//
//	import (
//	    "github.com/exapsy/chainkit"
//	    "github.com/exapsy/chainkit/bitcoin/providers"
//	    "github.com/exapsy/chainkit/bitcoin/types"
//	)
//
//	metal := providers.NewMetal(types.BitcoinNetworkMainnet)
//	mempool := providers.NewMempool(types.BitcoinNetworkMainnet, "https://mempool.space/api")
//
//	client := chainkit.NewMixedProvidersBuilder().
//	    WithAddressGeneratorChain(chainkit.AddressGeneratorConfig{Generator: metal, Priority: 1}).
//	    WithTxAssemblerChain(chainkit.TxAssemblerConfig{Assembler: metal, Priority: 1}).
//	    WithTxSignerChain(chainkit.TxSignerConfig{Signer: metal, Priority: 1}).
//	    WithTxSizerChain(chainkit.TxSizerConfig{Sizer: metal, Priority: 1}).
//	    WithFeeEstimatorChain(chainkit.FeeEstimatorConfig{Estimator: metal, Priority: 1}).
//	    WithFeeRecommenderChain(chainkit.FeeRecommenderConfig{Recommender: mempool, Priority: 1}).
//	    WithBalanceFetcherChain(chainkit.BalanceFetcherConfig{Fetcher: mempool, Priority: 1}).
//	    WithUTXOFetcherChain(chainkit.UTXOFetcherConfig{Fetcher: mempool, Priority: 1}).
//	    WithRateFetcherChain(chainkit.RateFetcherConfig{Fetcher: mempool, Priority: 1}).
//	    WithTxBroadcasterChain(chainkit.TxBroadcasterConfig{Broadcaster: mempool, Priority: 1}).
//	    WithTxStatusFetcherChain(chainkit.TxStatusFetcherConfig{Fetcher: mempool, Priority: 1}).
//	    Build()
//
// Not all roles are required. Only register the capabilities you actually use.
// If you call a method whose role has no registered provider, you receive an
// [ErrProviderNotConfigured] error.
package chainkit

import (
	"context"
	"errors"
	"time"

	"github.com/exapsy/chainkit/bitcoin/types"
)

// ErrProviderNotConfigured is returned when a method is called for a capability
// that has no provider registered in the builder.
var ErrProviderNotConfigured = errors.New("provider not configured")

// Context keys for provider tracking.
type contextKey string

const (
	// ProviderNameKey is used to store the provider name in the context.
	ProviderNameKey contextKey = "provider_name"
)

// WithProviderName adds the provider name to the context.
func WithProviderName(ctx context.Context, providerName string) context.Context {
	return context.WithValue(ctx, ProviderNameKey, providerName)
}

// GetProviderName retrieves the provider name from the context.
func GetProviderName(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(ProviderNameKey).(string)
	return name, ok
}

// AddressValidator checks whether a Bitcoin address is valid on the configured network.
type AddressValidator interface {
	ValidateAddress(ctx context.Context, address string) (bool, error)
}

// DerivedAddressMode indicates whether a derived address includes its private key.
type DerivedAddressMode int

const (
	PublicKeyOnly      DerivedAddressMode = iota
	PublicAndPrivateKey
)

// DerivedAddress holds the result of an HD-wallet address derivation.
type DerivedAddress struct {
	PublicKey  string
	PrivateKey string
	Mode       DerivedAddressMode
}

// AddressGenerator derives Bitcoin addresses from an extended public (or private) key.
type AddressGenerator interface {
	DeriveAddress(ctx context.Context, xpub string, index uint32, childIndex uint32) (DerivedAddress, error)
}

// FeeRecommender fetches current fee-rate recommendations from the network.
// Use [FeeEstimator] to compute the actual fee amount for a given transaction size.
type FeeRecommender interface {
	GetTxFees(ctx context.Context) ([]types.FeeTier, error)
	// GetTxFee returns the fee tier that best matches the requested priority.
	// Each provider adapter maps the named priority to its own internal API.
	GetTxFee(ctx context.Context, priority types.FeePriority) (types.FeeTier, error)
}

// FeeEstimator calculates the fee amount (in satoshis) for a transaction of a given
// size and fee rate. This is a local, offline calculation — no network call is made.
type FeeEstimator interface {
	CalculateFee(ctx context.Context, txSize uint64, feePerByte uint64) (uint64, error)
}

// TxAssembler constructs an unsigned [types.Tx] from UTXOs and desired outputs.
type TxAssembler interface {
	CreateTransaction(ctx context.Context, utxos []types.UTXO, outputs []types.TxOutput) (*types.Tx, error)
}

// TxSizer reports the serialized byte-size of a signed transaction.
type TxSizer interface {
	CalculateTransactionSize(ctx context.Context, tx *types.SignedTx) (uint64, error)
}

// TxSigner signs a [types.Tx] using the supplied WIF-encoded private key.
type TxSigner interface {
	SignTransaction(ctx context.Context, tx *types.Tx, utxos []types.UTXO, privWIF string) (*types.SignedTx, error)
}

// UTXOFetcher retrieves unspent transaction outputs for a Bitcoin address.
type UTXOFetcher interface {
	GetUTXOs(ctx context.Context, address string) ([]types.UTXO, error)
}

// TxBroadcaster submits a signed raw transaction to the Bitcoin network.
type TxBroadcaster interface {
	PushTx(ctx context.Context, rawTx []byte) (txID string, err error)
}

// TxStatusResponse contains transaction confirmation information.
type TxStatusResponse struct {
	Confirmed     bool
	Confirmations int
	BlockHeight   int64
	BlockHash     string
}

// TxStatusFetcher checks the confirmation status of a transaction by its ID.
type TxStatusFetcher interface {
	GetTxStatus(ctx context.Context, txID string) (*TxStatusResponse, error)
}

// GetBalanceOptions allows callers to pass pre-fetched UTXOs to avoid a redundant
// network round-trip when the UTXOs are already available.
type GetBalanceOptions struct {
	UTXOs []types.UTXO
}

// BalanceFetcher retrieves the balance for a Bitcoin address.
type BalanceFetcher interface {
	GetBalance(ctx context.Context, address string, opts *GetBalanceOptions) (uint64, error)
	GetConfirmedBalance(ctx context.Context, address string) (uint64, error)
	GetUnconfirmedBalance(ctx context.Context, address string) (uint64, error)
}

// RateFetcher fetches fiat exchange rates for a cryptocurrency.
type RateFetcher interface {
	GetExchangeRate(ctx context.Context, coin types.CoinTicker, currency types.Currency) (*types.CoinRate, error)
	GetExchangeRates(ctx context.Context, coin types.CoinTicker) ([]types.CoinRate, error)
}

// ProviderCapability is a string tag describing a single capability a provider implements.
type ProviderCapability string

const (
	CapabilityAddressGeneration  ProviderCapability = "address_generation"
	CapabilityAddressValidation  ProviderCapability = "address_validation"
	CapabilityBalanceFetching    ProviderCapability = "balance_fetching"
	CapabilityFeeRecommending    ProviderCapability = "fee_recommending" // FeeRecommender
	CapabilityFeeEstimation      ProviderCapability = "fee_estimation"   // FeeEstimator
	CapabilityRateFetching       ProviderCapability = "rate_fetching"
	CapabilityTxAssembly         ProviderCapability = "tx_assembly"
	CapabilityTxBroadcast        ProviderCapability = "tx_broadcast"
	CapabilityTxSigning          ProviderCapability = "tx_signing"
	CapabilityTxSizing           ProviderCapability = "tx_sizing"
	CapabilityTxStatusFetching   ProviderCapability = "tx_status_fetching"
	CapabilityUTXOFetching       ProviderCapability = "utxo_fetching"
)

// HealthStatus describes the current health of a provider.
type HealthStatus struct {
	Status         string    // "healthy", "degraded", or "down"
	ResponseTimeMs int64
	ResponseTimeUs int64
	HTTPStatus     int
	Error          string
	LastChecked    time.Time
}

// HealthChecker can report its own health status.
type HealthChecker interface {
	CheckHealth(ctx context.Context) HealthStatus
}

// CapabilityReporter can enumerate the capabilities it implements.
type CapabilityReporter interface {
	GetCapabilities() []ProviderCapability
}

// BlockchainBaseProvider is the minimal interface every provider must satisfy.
type BlockchainBaseProvider interface {
	Name() string
}

// BlockchainProvider is the aggregate interface implemented by [MixedProviders].
// It is the type returned by [MixedProvidersBuilder.Build].
//
// For function parameters, prefer the focused sub-interfaces (e.g. [BalanceFetcher],
// [RateFetcher], [UTXOFetcher]) rather than accepting the full BlockchainProvider —
// this makes dependencies explicit and individual providers easier to satisfy.
type BlockchainProvider interface {
	AddressGenerator
	FeeRecommender
	FeeEstimator
	TxBroadcaster
	TxAssembler
	TxSizer
	TxSigner
	UTXOFetcher
	BalanceFetcher
	RateFetcher
	AddressValidator
	TxStatusFetcher

	// GetBalanceWithContext is like GetBalance but also returns the updated context
	// carrying the name of the provider that served the request.
	GetBalanceWithContext(ctx context.Context, address string, opts *GetBalanceOptions) (context.Context, uint64, error)
}
