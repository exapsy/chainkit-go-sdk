package chainkit

import (
	"context"
	"fmt"

	"github.com/exapsy/chainkit/bitcoin/types"
)

// MixedProviders implements BlockchainProvider using multiple provider chains with fallback support
type MixedProviders struct {
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
	metricsRecorder   MetricsRecorder
}

func (m *MixedProviders) Name() string {
	return "MixedProviders"
}

// Helper function to execute operations with fallback between providers with metrics tracking
func executeWithFallbackAndMetrics[ResultT any](
	ctx context.Context,
	manager *ProviderManager,
	metricsRecorder MetricsRecorder,
	operation string,
	fn func(any) (ResultT, error),
) (ResultT, error) {
	var zero ResultT
	providers := manager.GetAvailableProviders()

	if len(providers) == 0 {
		return zero, fmt.Errorf("%w: no provider registered for %s", ErrProviderNotConfigured, operation)
	}

	result, providerName, duration, err := manager.RunOp(ctx, func(ctx context.Context, provider interface{}) (interface{}, error) { return fn(provider) })
	if err != nil {
		return zero, err
	}

	// Record metrics for this provider call
	metricsRecorder.RecordBlockchainRequest(ctx, providerName, operation, true, duration)

	return result.(ResultT), nil
}

// executeWithFallbackAndMetricsWithContext is like executeWithFallbackAndMetrics but returns updated context
func executeWithFallbackAndMetricsWithContext[ResultT any](
	ctx context.Context,
	manager *ProviderManager,
	metricsRecorder MetricsRecorder,
	operation string,
	fn func(any) (ResultT, error),
) (context.Context, ResultT, error) {
	var zero ResultT
	providers := manager.GetAvailableProviders()

	if len(providers) == 0 {
		return ctx, zero, fmt.Errorf("%w: no provider registered for %s", ErrProviderNotConfigured, operation)
	}

	result, providerName, duration, err := manager.RunOp(ctx, func(ctx context.Context, provider interface{}) (interface{}, error) { return fn(provider) })

	// Set the provider name in the context so callers can access it
	ctx = WithProviderName(ctx, providerName)

	if err != nil {
		return ctx, zero, err
	}

	// Record metrics for this provider call
	success := true
	metricsRecorder.RecordBlockchainRequest(ctx, providerName, operation, success, duration)

	if !success {
		return ctx, zero, fmt.Errorf("operation %s failed on provider %s: %w", operation, providerName, err)
	}

	return ctx, result.(ResultT), nil
}

// ProviderExists reports whether a provider with the given name is registered
// in any of the role chains.
func (m *MixedProviders) ProviderExists(name string) bool {
	for _, manager := range []*ProviderManager{
		m.addressGenerators,
		m.addressValidators,
		m.txMonitors,
		m.feeEstimators,
		m.feeRecommenders,
		m.txBroadcasters,
		m.utxoFetchers,
		m.txAssemblers,
		m.txSizers,
		m.txSigners,
		m.txStatusFetchers,
		m.balanceFetchers,
		m.rateFetchers,
	} {
		if manager != nil && manager.HasProvider(name) {
			return true
		}
	}
	return false
}

func (m *MixedProviders) DeriveAddress(ctx context.Context, xpub string, index uint32, childIndex uint32) (DerivedAddress, error) {
	return executeWithFallbackAndMetrics(ctx, m.addressGenerators, m.metricsRecorder, "DeriveAddress", func(provider interface{}) (DerivedAddress, error) {
		return provider.(AddressGenerator).DeriveAddress(ctx, xpub, index, childIndex)
	})
}

func (m *MixedProviders) GetTxFees(ctx context.Context) ([]types.FeeTier, error) {
	return executeWithFallbackAndMetrics(ctx, m.feeRecommenders, m.metricsRecorder, "GetTxFees", func(provider interface{}) ([]types.FeeTier, error) {
		return provider.(FeeRecommender).GetTxFees(ctx)
	})
}

func (m *MixedProviders) GetTxFee(ctx context.Context, feeTier int) (types.FeeTier, error) {
	return executeWithFallbackAndMetrics(ctx, m.feeRecommenders, m.metricsRecorder, "GetTxFee", func(provider interface{}) (types.FeeTier, error) {
		return provider.(FeeRecommender).GetTxFee(ctx, feeTier)
	})
}

func (m *MixedProviders) CalculateFee(ctx context.Context, txSize uint64, feePerByte uint64) (uint64, error) {
	return executeWithFallbackAndMetrics(ctx, m.feeEstimators, m.metricsRecorder, "CalculateFee", func(provider interface{}) (uint64, error) {
		return provider.(FeeEstimator).CalculateFee(ctx, txSize, feePerByte)
	})
}

func (m *MixedProviders) PushTx(ctx context.Context, rawTx []byte) (string, error) {
	return executeWithFallbackAndMetrics(ctx, m.txBroadcasters, m.metricsRecorder, "PushTx", func(provider interface{}) (string, error) {
		return provider.(TxBroadcaster).PushTx(ctx, rawTx)
	})
}

func (m *MixedProviders) CreateTransaction(ctx context.Context, utxos []types.UTXO, outputs []types.TxOutput) (*types.Tx, error) {
	return executeWithFallbackAndMetrics(ctx, m.txAssemblers, m.metricsRecorder, "CreateTransaction", func(provider interface{}) (*types.Tx, error) {
		return provider.(TxAssembler).CreateTransaction(ctx, utxos, outputs)
	})
}

func (m *MixedProviders) CalculateTransactionSize(ctx context.Context, tx *types.SignedTx) (uint64, error) {
	return executeWithFallbackAndMetrics(ctx, m.txSizers, m.metricsRecorder, "CalculateTransactionSize", func(provider interface{}) (uint64, error) {
		return provider.(TxSizer).CalculateTransactionSize(ctx, tx)
	})
}

func (m *MixedProviders) SignTransaction(ctx context.Context, tx *types.Tx, utxos []types.UTXO, privWIF string) (*types.SignedTx, error) {
	return executeWithFallbackAndMetrics(ctx, m.txSigners, m.metricsRecorder, "SignTransaction", func(provider interface{}) (*types.SignedTx, error) {
		return provider.(TxSigner).SignTransaction(ctx, tx, utxos, privWIF)
	})
}

func (m *MixedProviders) GetUTXOs(ctx context.Context, address string) ([]types.UTXO, error) {
	return executeWithFallbackAndMetrics(ctx, m.utxoFetchers, m.metricsRecorder, "GetUTXOs", func(provider interface{}) ([]types.UTXO, error) {
		return provider.(UTXOFetcher).GetUTXOs(ctx, address)
	})
}

func (m *MixedProviders) GetBalance(ctx context.Context, address string, opts *GetBalanceOptions) (uint64, error) {
	_, result, err := executeWithFallbackAndMetricsWithContext(ctx, m.balanceFetchers, m.metricsRecorder, "GetBalance", func(provider interface{}) (uint64, error) {
		return provider.(BalanceFetcher).GetBalance(ctx, address, opts)
	})
	return result, err
}

// GetBalanceWithContext is like GetBalance but returns the updated context with provider info
func (m *MixedProviders) GetBalanceWithContext(ctx context.Context, address string, opts *GetBalanceOptions) (context.Context, uint64, error) {
	return executeWithFallbackAndMetricsWithContext(ctx, m.balanceFetchers, m.metricsRecorder, "GetBalance", func(provider interface{}) (uint64, error) {
		return provider.(BalanceFetcher).GetBalance(ctx, address, opts)
	})
}

func (m *MixedProviders) GetConfirmedBalance(ctx context.Context, address string) (uint64, error) {
	return executeWithFallbackAndMetrics(ctx, m.balanceFetchers, m.metricsRecorder, "GetConfirmedBalance", func(provider interface{}) (uint64, error) {
		return provider.(BalanceFetcher).GetConfirmedBalance(ctx, address)
	})
}

func (m *MixedProviders) GetUnconfirmedBalance(ctx context.Context, address string) (uint64, error) {
	return executeWithFallbackAndMetrics(ctx, m.balanceFetchers, m.metricsRecorder, "GetUnconfirmedBalance", func(provider interface{}) (uint64, error) {
		return provider.(BalanceFetcher).GetUnconfirmedBalance(ctx, address)
	})
}

func (m *MixedProviders) ValidateAddress(ctx context.Context, address string) (bool, error) {
	return executeWithFallbackAndMetrics(ctx, m.addressValidators, m.metricsRecorder, "ValidateAddress", func(provider interface{}) (bool, error) {
		if validator, ok := provider.(AddressValidator); ok {
			return validator.ValidateAddress(ctx, address)
		}
		// If provider doesn't support address validation, consider it valid for now
		return true, nil
	})
}

func (m *MixedProviders) GetExchangeRate(ctx context.Context, coin types.CoinTicker, currency types.Currency) (*types.CoinRate, error) {
	return executeWithFallbackAndMetrics(ctx, m.rateFetchers, m.metricsRecorder, "GetExchangeRate", func(provider interface{}) (*types.CoinRate, error) {
		return provider.(RateFetcher).GetExchangeRate(ctx, coin, currency)
	})
}

func (m *MixedProviders) GetExchangeRates(ctx context.Context, coin types.CoinTicker) ([]types.CoinRate, error) {
	return executeWithFallbackAndMetrics(ctx, m.rateFetchers, m.metricsRecorder, "GetExchangeRates", func(provider interface{}) ([]types.CoinRate, error) {
		return provider.(RateFetcher).GetExchangeRates(ctx, coin)
	})
}

func (m *MixedProviders) GetTxStatus(ctx context.Context, txID string) (*TxStatusResponse, error) {
	return executeWithFallbackAndMetrics(ctx, m.txStatusFetchers, m.metricsRecorder, "GetTxStatus", func(provider interface{}) (*TxStatusResponse, error) {
		return provider.(TxStatusFetcher).GetTxStatus(ctx, txID)
	})
}
