package providers

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
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
	// Network is the Bitcoin network to connect to. The provider derives the
	// Blockcypher chain identifier ("main"/"test3") from this value internally.
	// Blockcypher supports mainnet, testnet3, and testnet4 (mapped to test3).
	Network types.BitcoinNetwork
}

// NewBlockcypher creates a BlockcypherProvider for the given network.
// Returns nil if the network is not supported by Blockcypher (regtest, simnet).
func NewBlockcypher(options BlockcypherProviderOptions) BlockcypherProvider {
	chain, ok := options.Network.BlockcypherChain()
	if !ok {
		return nil
	}
	return &blockcypher{
		Client: gobcy.API{
			Token: options.APIKey,
			Chain: chain,
			Coin:  "btc",
		},
	}
}

func (p *blockcypher) Name() string {
	return "Blockcypher"
}

func (p *blockcypher) GetUTXOs(ctx context.Context, address string) ([]types.UTXO, error) {
	flags := map[string]string{
		"unspent": "true",
	}

	addr, err := p.Client.GetAddr(address, flags)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch address details: %w", err)
	}

	utxos := make([]types.UTXO, 0, len(addr.TXRefs))
	for _, txRef := range addr.TXRefs {
		scriptBytes, _ := hex.DecodeString(txRef.Script)
		utxos = append(utxos, types.UTXO{
			TxHash:        txRef.TXHash,
			Vout:          uint32(txRef.TXOutputN),
			Amount:        txRef.Value.Int64(),
			Confirmed:     txRef.Confirmations > 0,
			Confirmations: int64(txRef.Confirmations),
			ScriptPubKey:  scriptBytes,
			Address:       address,
		})
	}

	return utxos, nil
}

func (p *blockcypher) ValidateAddress(ctx context.Context, address string) (bool, error) {
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

// GetBalance fetches address details once and returns confirmed, unconfirmed, and total balance.
func (p *blockcypher) GetBalance(ctx context.Context, address string) (chainkit.Balance, error) {
	addr, err := p.Client.GetAddr(address, nil)
	if err != nil {
		return chainkit.Balance{}, fmt.Errorf("failed to fetch address details: %w", err)
	}

	confirmed := addr.Balance.Int64()
	unconfirmed := addr.UnconfirmedBalance.Int64()
	if confirmed < 0 {
		confirmed = 0
	}
	if unconfirmed < 0 {
		unconfirmed = 0
	}

	return chainkit.Balance{
		Confirmed:   uint64(confirmed),
		Unconfirmed: uint64(unconfirmed),
		Total:       uint64(confirmed + unconfirmed),
	}, nil
}

// CheckHealth performs a health check on the BlockCypher API
func (p *blockcypher) CheckHealth(ctx context.Context) chainkit.HealthStatus {
	start := time.Now()

	if p.Client.Chain == "" || p.Client.Coin == "" {
		return chainkit.HealthStatus{
			Status: chainkit.HealthLevelDown,
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
			Status: chainkit.HealthLevelDown,
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
			Status: chainkit.HealthLevelDown,
			ResponseTimeMs: responseTimeMs,
			ResponseTimeUs: responseTimeUs,
			Error:          err.Error(),
			LastChecked:    time.Now(),
		}
	}
	defer resp.Body.Close()

	status := chainkit.HealthLevelHealthy
	errorMsg := ""

	if resp.StatusCode >= 500 {
		status = chainkit.HealthLevelDown
		errorMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
	} else if resp.StatusCode >= 400 {
		status = chainkit.HealthLevelDegraded
		errorMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
	} else if responseTimeMs > 2000 {
		status = chainkit.HealthLevelDegraded
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
