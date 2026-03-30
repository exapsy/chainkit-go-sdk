package chainkit

import (
	"context"

	"github.com/exapsy/chainkit/bitcoin/types"
)

// ProviderSelector wraps a [BlockchainProvider] and pins all calls to a specific
// named provider. If selectedProvider is empty all calls pass through to the
// mixed-provider chain unchanged.
type ProviderSelector struct {
	mixedProvider    BlockchainProvider
	selectedProvider string
}

// NewProviderSelector creates a new provider selector
func NewProviderSelector(mixedProvider BlockchainProvider, selectedProvider string) *ProviderSelector {
	return &ProviderSelector{
		mixedProvider:    mixedProvider,
		selectedProvider: selectedProvider,
	}
}

// ctxWithProvider returns ctx with the selected provider name injected, if one is set.
func (ps *ProviderSelector) ctxWithProvider(ctx context.Context) context.Context {
	if ps.selectedProvider != "" {
		return WithProviderName(ctx, ps.selectedProvider)
	}
	return ctx
}

func (ps *ProviderSelector) GetBalance(ctx context.Context, address string, opts *GetBalanceOptions) (uint64, error) {
	return ps.mixedProvider.GetBalance(ps.ctxWithProvider(ctx), address, opts)
}

func (ps *ProviderSelector) GetBalanceWithContext(ctx context.Context, address string, opts *GetBalanceOptions) (context.Context, uint64, error) {
	return ps.mixedProvider.GetBalanceWithContext(ps.ctxWithProvider(ctx), address, opts)
}

func (ps *ProviderSelector) GetConfirmedBalance(ctx context.Context, address string) (uint64, error) {
	return ps.mixedProvider.GetConfirmedBalance(ps.ctxWithProvider(ctx), address)
}

func (ps *ProviderSelector) GetUnconfirmedBalance(ctx context.Context, address string) (uint64, error) {
	return ps.mixedProvider.GetUnconfirmedBalance(ps.ctxWithProvider(ctx), address)
}

func (ps *ProviderSelector) GetUTXOs(ctx context.Context, address string) ([]types.UTXO, error) {
	return ps.mixedProvider.GetUTXOs(ps.ctxWithProvider(ctx), address)
}

func (ps *ProviderSelector) GetTxFees(ctx context.Context) ([]types.FeeTier, error) {
	return ps.mixedProvider.GetTxFees(ps.ctxWithProvider(ctx))
}

func (ps *ProviderSelector) GetTxFee(ctx context.Context, priority types.FeePriority) (types.FeeTier, error) {
	return ps.mixedProvider.GetTxFee(ps.ctxWithProvider(ctx), priority)
}

func (ps *ProviderSelector) CalculateFee(ctx context.Context, txSize uint64, feePerByte uint64) (uint64, error) {
	return ps.mixedProvider.CalculateFee(ps.ctxWithProvider(ctx), txSize, feePerByte)
}

func (ps *ProviderSelector) PushTx(ctx context.Context, rawTx []byte) (string, error) {
	return ps.mixedProvider.PushTx(ps.ctxWithProvider(ctx), rawTx)
}

func (ps *ProviderSelector) CreateTransaction(ctx context.Context, utxos []types.UTXO, outputs []types.TxOutput) (*types.Tx, error) {
	return ps.mixedProvider.CreateTransaction(ps.ctxWithProvider(ctx), utxos, outputs)
}

func (ps *ProviderSelector) CalculateTransactionSize(ctx context.Context, tx *types.SignedTx) (uint64, error) {
	return ps.mixedProvider.CalculateTransactionSize(ps.ctxWithProvider(ctx), tx)
}

func (ps *ProviderSelector) SignTransaction(ctx context.Context, tx *types.Tx, utxos []types.UTXO, privWIF string) (*types.SignedTx, error) {
	return ps.mixedProvider.SignTransaction(ps.ctxWithProvider(ctx), tx, utxos, privWIF)
}

func (ps *ProviderSelector) DeriveAddress(ctx context.Context, xpub string, index uint32, childIndex uint32) (DerivedAddress, error) {
	return ps.mixedProvider.DeriveAddress(ps.ctxWithProvider(ctx), xpub, index, childIndex)
}

func (ps *ProviderSelector) ValidateAddress(ctx context.Context, address string) (bool, error) {
	return ps.mixedProvider.ValidateAddress(ps.ctxWithProvider(ctx), address)
}

func (ps *ProviderSelector) GetExchangeRate(ctx context.Context, coin types.CoinTicker, currency types.Currency) (*types.CoinRate, error) {
	return ps.mixedProvider.GetExchangeRate(ps.ctxWithProvider(ctx), coin, currency)
}

func (ps *ProviderSelector) GetExchangeRates(ctx context.Context, coin types.CoinTicker) ([]types.CoinRate, error) {
	return ps.mixedProvider.GetExchangeRates(ps.ctxWithProvider(ctx), coin)
}

func (ps *ProviderSelector) GetTxStatus(ctx context.Context, txID string) (*TxStatusResponse, error) {
	return ps.mixedProvider.GetTxStatus(ps.ctxWithProvider(ctx), txID)
}
