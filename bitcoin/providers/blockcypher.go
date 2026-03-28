package providers

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/blockcypher/gobcy/v2"
	"github.com/exapsy/chainkit"
	"github.com/exapsy/chainkit/bitcoin/types"
)

type BlockcypherProvider interface {
	chainkit.BlockchainBaseProvider
	chainkit.BalanceFetcher
	chainkit.UTXOFetcher
	chainkit.AddressValidator
}

type blockcypher struct {
	Client gobcy.API
}

type BlockcypherProviderOptions struct {
	APIKey string
	// Chain is the Blockcypher chain identifier for the active network,
	// e.g. "main" for mainnet, "test3" for testnet3.
	// This value comes directly from config/providers/blockcypher.yaml
	// and must be resolved by the caller before constructing the provider.
	Chain string
	// Coin is the Blockcypher coin identifier, e.g. "btc".
	Coin string
}

func NewBlockcypher(options BlockcypherProviderOptions) BlockcypherProvider {
	return &blockcypher{
		Client: gobcy.API{
			Token: options.APIKey,
			Chain: options.Chain,
			Coin:  options.Coin,
		},
	}
}

func (p *blockcypher) Name() string {
	return "Blockcypher"
}

func (p *blockcypher) GetUTXOs(ctx context.Context, address string) ([]types.UTXO, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	flags := map[string]string{
		"unspent": "true",
	}

	addr, err := p.Client.GetAddr(address, flags)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch address details: %w", err)
	}

	utxos := make([]types.UTXO, 0, len(addr.TXRefs))
	for _, txRef := range addr.TXRefs {
		utxos = append(utxos, types.UTXO{
			TxHash:    txRef.TXHash,
			Amount:    txRef.Value.Int64(),
			Confirmed: txRef.Confirmations > 0,
		})
	}

	return utxos, nil
}

func (p *blockcypher) ValidateAddress(ctx context.Context, address string) (bool, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	// Use BlockCypher's GetAddr endpoint to validate the address
	// If the address is invalid, BlockCypher will return an error
	_, err := p.Client.GetAddr(address, nil)
	if err != nil {
		// Check if it's an address-related error (usually contains "address" in the message)
		errStr := err.Error()
		if strings.Contains(strings.ToLower(errStr), "address") ||
			strings.Contains(strings.ToLower(errStr), "invalid") ||
			strings.Contains(strings.ToLower(errStr), "not found") {
			return false, nil // Address is invalid but no error occurred in the validation process
		}
		return false, fmt.Errorf("failed to validate address with BlockCypher: %w", err)
	}

	return true, nil
}

func (p *blockcypher) GetBalance(
	ctx context.Context,
	address string,
	opts *chainkit.GetBalanceOptions,
) (uint64, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	// If UTXOs are provided, calculate balance from them
	if opts != nil && len(opts.UTXOs) > 0 {
		balance, err := getBalanceByUTXOs(opts.UTXOs)
		if err != nil {
			return 0, fmt.Errorf("failed to calculate balance from UTXOs: %w", err)
		}

		return balance, nil
	}

	addr, err := p.Client.GetAddr(address, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch address details: %w", err)
	}

	totalBalance := new(big.Int).Add(&addr.Balance, &addr.UnconfirmedBalance)

	return uint64(totalBalance.Int64()), nil
}

func (p *blockcypher) GetConfirmedBalance(ctx context.Context, address string) (uint64, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	addr, err := p.Client.GetAddr(address, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch address details: %w", err)
	}

	return uint64(addr.Balance.Int64()), nil
}

func (p *blockcypher) GetUnconfirmedBalance(ctx context.Context, address string) (uint64, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	addr, err := p.Client.GetAddr(address, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch address details: %w", err)
	}

	return uint64(addr.UnconfirmedBalance.Int64()), nil
}

// CheckHealth performs a health check on the BlockCypher API
func (p *blockcypher) CheckHealth(ctx context.Context) chainkit.HealthStatus {
	start := time.Now()

	if p.Client.Chain == "" || p.Client.Coin == "" {
		return chainkit.HealthStatus{
			Status:         "error",
			ResponseTimeMs: 0,
			ResponseTimeUs: 0,
			Error:          "Blockcypher.com only supports Bitcoin mainnet and testnet3",
			LastChecked:    time.Now(),
		}
	}

	// Construct URL based on network and coin
	url := fmt.Sprintf("https://api.blockcypher.com/v1/%s/%s", p.Client.Coin, p.Client.Chain)
	if p.Client.Token != "" {
		url = fmt.Sprintf("%s?token=%s", url, p.Client.Token)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return chainkit.HealthStatus{
			Status:         "error",
			ResponseTimeMs: 0,
			ResponseTimeUs: 0,
			Error:          err.Error(),
			LastChecked:    time.Now(),
		}
	}

	resp, err := client.Do(req)
	responseDuration := time.Since(start)
	responseTimeMs := responseDuration.Milliseconds()
	responseTimeUs := responseDuration.Microseconds()

	if err != nil {
		return chainkit.HealthStatus{
			Status:         "down",
			ResponseTimeMs: responseTimeMs,
			ResponseTimeUs: responseTimeUs,
			Error:          err.Error(),
			LastChecked:    time.Now(),
		}
	}
	defer resp.Body.Close()

	status := "healthy"
	errorMsg := ""

	if resp.StatusCode >= 500 {
		status = "down"
		errorMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
	} else if resp.StatusCode >= 400 {
		status = "degraded"
		errorMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
	} else if responseTimeMs > 2000 {
		status = "degraded"
		errorMsg = "slow response"
	}

	return chainkit.HealthStatus{
		Status:         status,
		ResponseTimeMs: responseTimeMs,
		ResponseTimeUs: responseTimeUs,
		HTTPStatus:     resp.StatusCode,
		Error:          errorMsg,
		LastChecked:    time.Now(),
	}
}

// GetCapabilities returns the list of capabilities this provider supports
func (p *blockcypher) GetCapabilities() []chainkit.ProviderCapability {
	return []chainkit.ProviderCapability{
		chainkit.CapabilityBalanceFetching,
		chainkit.CapabilityUTXOFetching,
		chainkit.CapabilityAddressValidation,
	}
}

func getBalanceByUTXOs(utxos []types.UTXO) (uint64, error) {
	if len(utxos) == 0 {
		return 0, errors.New("no UTXOs provided")
	}

	totalBalance := uint64(0)
	for _, utxo := range utxos {
		totalBalance += uint64(utxo.Amount)
	}

	return totalBalance, nil
}
