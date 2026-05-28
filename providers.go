package chainkit

import (
	"context"
	"time"

	"github.com/exapsy/chainkit/bitcoin/types"
	"github.com/exapsy/chainkit/scoring"
)

// MixedProviders implements BlockchainProvider using multiple provider chains with fallback support
type MixedProviders struct {
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
	historicalRateFetchers *providerManager
	metricsRecorder   MetricsRecorder
	scoringEngine     *scoring.Engine
}

func (m *MixedProviders) Name() string {
	return "MixedProviders"
}

// executeWithFallbackAndMetrics runs fn through the provider chain with fallback and metrics.
//
// The metrics recorder is invoked exactly once per call. When the recorder satisfies
// RichMetricsRecorder, the rich variant is called with a structured RequestEvent;
// otherwise the simple RecordBlockchainRequest is called. This keeps existing
// MetricsRecorder implementations working unchanged while letting newer recorders
// receive attempt count and classified error.
func executeWithFallbackAndMetrics[ResultT any](
	ctx context.Context,
	manager *providerManager,
	metricsRecorder MetricsRecorder,
	operation string,
	fn func(any) (ResultT, error),
) (ResultT, error) {
	var zero ResultT

	result, providerName, duration, attempts, err := manager.runOp(ctx, func(ctx context.Context, provider interface{}) (interface{}, error) { return fn(provider) })

	if rich, ok := metricsRecorder.(RichMetricsRecorder); ok {
		rich.RecordBlockchainRequestRich(ctx, RequestEvent{
			Provider:     providerName,
			Operation:    operation,
			Success:      err == nil,
			Duration:     duration,
			AttemptCount: attempts,
			ErrorClass:   classifyError(err),
		})
	} else {
		metricsRecorder.RecordBlockchainRequest(ctx, providerName, operation, err == nil, duration)
	}

	if err != nil {
		return zero, err
	}

	return result.(ResultT), nil
}

// ProviderExists reports whether a provider with the given name is registered
// in any of the role chains.
func (m *MixedProviders) ProviderExists(name string) bool {
	for _, manager := range []*providerManager{
		m.addressGenerators,
		m.addressValidators,
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

func (m *MixedProviders) GetTxFee(ctx context.Context, priority types.FeePriority) (types.FeeTier, error) {
	return executeWithFallbackAndMetrics(ctx, m.feeRecommenders, m.metricsRecorder, "GetTxFee", func(provider interface{}) (types.FeeTier, error) {
		return provider.(FeeRecommender).GetTxFee(ctx, priority)
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

func (m *MixedProviders) GetBalance(ctx context.Context, address string) (Balance, error) {
	return executeWithFallbackAndMetrics(ctx, m.balanceFetchers, m.metricsRecorder, "GetBalance", func(provider interface{}) (Balance, error) {
		return provider.(BalanceFetcher).GetBalance(ctx, address)
	})
}

func (m *MixedProviders) ValidateAddress(ctx context.Context, address string) (bool, error) {
	return executeWithFallbackAndMetrics(ctx, m.addressValidators, m.metricsRecorder, "ValidateAddress", func(provider interface{}) (bool, error) {
		return provider.(AddressValidator).ValidateAddress(ctx, address)
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

func (m *MixedProviders) GetHistoricalRates(ctx context.Context, coin types.CoinTicker, currency types.Currency, since, until time.Time) ([]types.CoinRate, error) {
	return executeWithFallbackAndMetrics(ctx, m.historicalRateFetchers, m.metricsRecorder, "GetHistoricalRates", func(provider interface{}) ([]types.CoinRate, error) {
		return provider.(HistoricalRateFetcher).GetHistoricalRates(ctx, coin, currency, since, until)
	})
}

func (m *MixedProviders) GetTxStatus(ctx context.Context, txID string) (*TxConfirmationStatus, error) {
	return executeWithFallbackAndMetrics(ctx, m.txStatusFetchers, m.metricsRecorder, "GetTxStatus", func(provider interface{}) (*TxConfirmationStatus, error) {
		return provider.(TxStatusFetcher).GetTxStatus(ctx, txID)
	})
}

// StopScoring stops the adaptive scoring engine's background processes.
// Call this when you're done using the MixedProviders to clean up goroutines.
func (m *MixedProviders) StopScoring() {
	if m.scoringEngine != nil {
		m.scoringEngine.Stop()
	}
}

// GetScoringEngine returns the scoring engine if adaptive scoring is enabled.
// Returns nil if adaptive scoring was not configured.
func (m *MixedProviders) GetScoringEngine() *scoring.Engine {
	return m.scoringEngine
}

// GetScoringStats returns scoring statistics for all providers.
// Returns nil if adaptive scoring was not configured.
func (m *MixedProviders) GetScoringStats() []scoring.ProviderScoreStats {
	if m.scoringEngine == nil {
		return nil
	}
	return m.scoringEngine.GetAllProviderStats()
}

// GetProviderScoringStats returns scoring statistics for a specific provider.
// Returns nil if adaptive scoring was not configured or provider not found.
func (m *MixedProviders) GetProviderScoringStats(providerName string) *scoring.ProviderScoreStats {
	if m.scoringEngine == nil {
		return nil
	}
	return m.scoringEngine.GetProviderStats(providerName)
}

// IsScoringEnabled returns true if adaptive scoring is enabled.
func (m *MixedProviders) IsScoringEnabled() bool {
	return m.scoringEngine != nil && m.scoringEngine.IsEnabled()
}

// SetScoringEnabled enables or disables adaptive scoring at runtime.
// Does nothing if no scoring engine was configured.
func (m *MixedProviders) SetScoringEnabled(enabled bool) {
	if m.scoringEngine != nil {
		m.scoringEngine.SetEnabled(enabled)
	}
}

// ResetScoring resets all provider scores to their base values.
// Useful for testing or when you want to start fresh.
func (m *MixedProviders) ResetScoring() {
	if m.scoringEngine != nil {
		m.scoringEngine.Reset()
	}
}

// GetSortedProviders returns provider names sorted by their current effective score.
// The first provider in the list has the highest score (best performance).
// Returns nil if adaptive scoring was not configured.
func (m *MixedProviders) GetSortedProviders() []string {
	if m.scoringEngine == nil {
		return nil
	}
	return m.scoringEngine.GetSortedProviders()
}
