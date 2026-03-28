package chainkit

import (
	"context"

	"github.com/exapsy/chainkit/bitcoin/types"
)

// ProviderSelector provides a way to select specific providers from the MixedProvider
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

// GetBalance gets balance using either the selected provider or the mixed provider chain
func (ps *ProviderSelector) GetBalance(ctx context.Context, address string, opts *GetBalanceOptions) (uint64, error) {
	return ps.mixedProvider.GetBalance(ps.ctxWithProvider(ctx), address, opts)
}

// GetConfirmedBalance gets confirmed balance
func (ps *ProviderSelector) GetConfirmedBalance(ctx context.Context, address string) (uint64, error) {
	return ps.mixedProvider.GetConfirmedBalance(ps.ctxWithProvider(ctx), address)
}

// GetUnconfirmedBalance gets unconfirmed balance
func (ps *ProviderSelector) GetUnconfirmedBalance(ctx context.Context, address string) (uint64, error) {
	return ps.mixedProvider.GetUnconfirmedBalance(ps.ctxWithProvider(ctx), address)
}

// FetchUTXOs fetches UTXOs for an address
func (ps *ProviderSelector) FetchUTXOs(ctx context.Context, address string) ([]types.UTXO, error) {
	return ps.mixedProvider.FetchUTXOs(ps.ctxWithProvider(ctx), address)
}

// GetTxFees gets transaction fees
func (ps *ProviderSelector) GetTxFees(ctx context.Context) ([]types.FeeTier, error) {
	return ps.mixedProvider.GetTxFees(ps.ctxWithProvider(ctx))
}

// ValidateAddress validates an address using either the selected provider or the mixed provider chain
func (ps *ProviderSelector) ValidateAddress(ctx context.Context, address string) (bool, error) {
	return ps.mixedProvider.ValidateAddress(ps.ctxWithProvider(ctx), address)
}
