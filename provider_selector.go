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

// GetBalance gets balance using either the selected provider or the mixed provider chain
func (ps *ProviderSelector) GetBalance(ctx context.Context, address string, opts *GetBalanceOptions) (uint64, error) {
	if ps.selectedProvider == "" {
		// Use the mixed provider chain (default behavior)
		return ps.mixedProvider.GetBalance(ctx, address, opts)
	}

	return ps.mixedProvider.GetBalance(ctx, address, opts)
}

// GetConfirmedBalance gets confirmed balance
func (ps *ProviderSelector) GetConfirmedBalance(ctx context.Context, address string) (uint64, error) {
	if ps.selectedProvider == "" {
		return ps.mixedProvider.GetConfirmedBalance(ctx, address)
	}
	return ps.mixedProvider.GetConfirmedBalance(ctx, address)
}

// GetUnconfirmedBalance gets unconfirmed balance
func (ps *ProviderSelector) GetUnconfirmedBalance(ctx context.Context, address string) (uint64, error) {
	if ps.selectedProvider == "" {
		return ps.mixedProvider.GetUnconfirmedBalance(ctx, address)
	}
	return ps.mixedProvider.GetUnconfirmedBalance(ctx, address)
}

// FetchUTXOs fetches UTXOs for an address
func (ps *ProviderSelector) FetchUTXOs(ctx context.Context, address string) ([]types.UTXO, error) {
	if ps.selectedProvider == "" {
		return ps.mixedProvider.FetchUTXOs(ctx, address)
	}
	return ps.mixedProvider.FetchUTXOs(ctx, address)
}

// GetTxFees gets transaction fees
func (ps *ProviderSelector) GetTxFees(ctx context.Context) ([]types.FeeTier, error) {
	if ps.selectedProvider == "" {
		return ps.mixedProvider.GetTxFees(ctx)
	}
	return ps.mixedProvider.GetTxFees(ctx)
}

// ValidateAddress validates an address using either the selected provider or the mixed provider chain
func (ps *ProviderSelector) ValidateAddress(ctx context.Context, address string) (bool, error) {
	if ps.selectedProvider == "" {
		// Use the mixed provider chain (default behavior)
		return ps.mixedProvider.ValidateAddress(ctx, address)
	}

	return ps.mixedProvider.ValidateAddress(ctx, address)
}
