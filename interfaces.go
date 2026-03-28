package chainkit

import (
	"context"
	"time"

	"github.com/exapsy/chainkit/bitcoin/types"
)

// Context keys for provider tracking
type contextKey string

const (
	// ProviderNameKey is used to store the provider name in the context
	ProviderNameKey contextKey = "provider_name"
)

// WithProviderName adds the provider name to the context
func WithProviderName(ctx context.Context, providerName string) context.Context {
	return context.WithValue(ctx, ProviderNameKey, providerName)
}

// GetProviderName retrieves the provider name from the context
func GetProviderName(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(ProviderNameKey).(string)
	return name, ok
}

type AddressValidator interface {
	ValidateAddress(ctx context.Context, address string) (bool, error)
}

type DerivedAddressMode int

const (
	PublicKeyOnly DerivedAddressMode = iota
	PublicAndPrivateKey
)

type DerivedAddress struct {
	PublicKey  string
	PrivateKey string
	Mode       DerivedAddressMode
}

type AddressGenerator interface {
	DeriveAddress(ctx context.Context, xpub string, index uint32, childIndex uint32) (DerivedAddress, error)
}

type FeeFetcher interface {
	GetTxFees(ctx context.Context) ([]types.FeeTier, error)
	GetTxFee(ctx context.Context, feeTier int) (types.FeeTier, error)
}

type FeeEstimator interface {
	CalculateFee(ctx context.Context, txSize uint64, feePerByte uint64) (uint64, error)
}

type TxAssembler interface {
	CreateTransaction(ctx context.Context, utxos []types.UTXO, outputs []types.TxOutput) (*types.Tx, error)
}

type TxSizer interface {
	CalculateTransactionSize(ctx context.Context, tx *types.SignedTx) (uint64, error)
}

type TxSigner interface {
	SignTransaction(ctx context.Context, tx *types.Tx, utxos []types.UTXO, privWIF string) (*types.SignedTx, error)
}

type UTXOFetcher interface {
	FetchUTXOs(ctx context.Context, address string) ([]types.UTXO, error)
}

type TxBroadcaster interface {
	PushTx(ctx context.Context, rawTx []byte) (txID string, err error)
}

// TxStatusResponse contains transaction status information
type TxStatusResponse struct {
	Confirmed     bool
	Confirmations int
	BlockHeight   int64
	BlockHash     string
}

// TxStatusFetcher interface for checking transaction confirmation status
type TxStatusFetcher interface {
	GetTxStatus(ctx context.Context, txID string) (*TxStatusResponse, error)
}

type GetBalanceOptions struct {
	// UTXOs can be provided to avoid fetching them again
	// and to use cached UTXOs.
	UTXOs []types.UTXO
}

type BalanceFetcher interface {
	GetBalance(ctx context.Context, address string, opts *GetBalanceOptions) (uint64, error)
	GetConfirmedBalance(ctx context.Context, address string) (uint64, error)
	GetUnconfirmedBalance(ctx context.Context, address string) (uint64, error)
}

type RateFetcher interface {
	GetExchangeRate(
		ctx context.Context,
		coin types.CoinTicker,
		currency types.Currency,
	) (*types.CoinRate, error)
	GetExchangeRates(ctx context.Context, coin types.CoinTicker) ([]types.CoinRate, error)
}

// ProviderCapability represents a capability/role that a blockchain provider implements
type ProviderCapability string

const (
	CapabilityAddressGeneration ProviderCapability = "address_generation"
	CapabilityAddressValidation ProviderCapability = "address_validation"
	CapabilityBalanceFetching   ProviderCapability = "balance_fetching"
	CapabilityFeeFetching       ProviderCapability = "fee_fetching"   // FeeFetcher - gets fee recommendations
	CapabilityFeeEstimation     ProviderCapability = "fee_estimation" // FeeEstimator - calculates fee amounts
	CapabilityRateFetching      ProviderCapability = "rate_fetching"
	CapabilityTxAssembly        ProviderCapability = "tx_assembly"
	CapabilityTxBroadcast       ProviderCapability = "tx_broadcast"
	CapabilityTxSigning         ProviderCapability = "tx_signing"
	CapabilityTxSizing          ProviderCapability = "tx_sizing"          // TxSizer - calculates transaction size
	CapabilityTxStatusFetching  ProviderCapability = "tx_status_fetching" // TxStatusFetcher - gets tx confirmation status
	CapabilityUTXOFetching      ProviderCapability = "utxo_fetching"
)

// HealthStatus represents the health status of a provider
type HealthStatus struct {
	Status         string    // "healthy", "degraded", "down"
	ResponseTimeMs int64     // Response time in milliseconds
	ResponseTimeUs int64     // Response time in microseconds
	HTTPStatus     int       // HTTP status code (if applicable)
	Error          string    // Error message (if any)
	LastChecked    time.Time // When the health check was performed
}

// HealthChecker interface for providers that can report their own health
type HealthChecker interface {
	CheckHealth(ctx context.Context) HealthStatus
}

// CapabilityReporter interface for providers that can report their capabilities
type CapabilityReporter interface {
	GetCapabilities() []ProviderCapability
}

type BlockchainBaseProvider interface {
	Name() string
}

type BlockchainProvider interface {
	AddressGenerator
	FeeFetcher
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
}
