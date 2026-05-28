package providers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
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
	chainkit.TxBroadcaster
	chainkit.TxStatusFetcher
	chainkit.APIKeyValidator
}

type blockcypher struct {
	Client gobcy.API

	// Auth state protected by mutex, updated by ValidateAPIKey()
	authMu    sync.RWMutex
	authValid *bool
	authErr   error
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
		return nil, fmt.Errorf("fetch address %s: %w", address, err)
	}

	// Blockcypher's TXRef is a union: each row represents EITHER an
	// input OR an output of a transaction touching this address. Rows
	// describing an input have TXOutputN = -1 and TXInputN >= 0; rows
	// describing an output flip those. Even with `unspent=true`, the
	// API returns both directions for some addresses (taproot in
	// particular, where heavy ordinal/rune activity produces lots of
	// inscription-driven traffic).
	//
	// Filter for actual output refs only — Spent=false (still
	// unspent) + TXOutputN >= 0 (it IS an output, not an input). Both
	// guards together prevent the int(-1) → uint32(4294967295)
	// disaster that otherwise surfaces phantom "UTXOs" at the same
	// (tx_hash, MAX_UINT32) coordinate, breaking any keyed
	// downstream consumer.
	utxos := make([]types.UTXO, 0, len(addr.TXRefs))
	for _, txRef := range addr.TXRefs {
		if txRef.Spent || txRef.TXOutputN < 0 {
			continue
		}
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
		return false, fmt.Errorf("validate address %s: %w", address, err)
	}

	return true, nil
}

// GetBalance fetches address details once and returns confirmed, unconfirmed, and total balance.
func (p *blockcypher) GetBalance(ctx context.Context, address string) (chainkit.Balance, error) {
	addr, err := p.Client.GetAddr(address, nil)
	if err != nil {
		return chainkit.Balance{}, fmt.Errorf("fetch balance for %s: %w", address, err)
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

// PushTx broadcasts a signed transaction to the Bitcoin network via BlockCypher's txs/push endpoint.
// rawTx must be the serialized transaction bytes; the method hex-encodes them before sending.
// Returns the txid on success.
func (p *blockcypher) PushTx(ctx context.Context, rawTx []byte) (string, error) {
	hexTx := hex.EncodeToString(rawTx)

	skel, err := p.Client.PushTX(hexTx)
	if err != nil {
		return "", fmt.Errorf("broadcast transaction: %w", err)
	}

	return skel.Trans.Hash, nil
}

// GetTxStatus returns the confirmation status of a transaction via BlockCypher's txs/{txid} endpoint.
func (p *blockcypher) GetTxStatus(ctx context.Context, txID string) (*chainkit.TxConfirmationStatus, error) {
	tx, err := p.Client.GetTX(txID, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch tx status %s: %w", txID, err)
	}

	confirmed := !tx.Confirmed.IsZero()

	blockHeight := int64(tx.BlockHeight)
	if blockHeight < 0 {
		blockHeight = 0
	}

	return &chainkit.TxConfirmationStatus{
		Confirmed:     confirmed,
		Confirmations: tx.Confirmations,
		BlockHeight:   blockHeight,
		BlockHash:     tx.BlockHash,
	}, nil
}

// CheckHealth performs a health check on the BlockCypher API
func (p *blockcypher) CheckHealth(ctx context.Context) chainkit.HealthStatus {
	start := time.Now()

	// Read current auth state
	p.authMu.RLock()
	authValid := p.authValid
	authErr := p.authErr
	p.authMu.RUnlock()

	if p.Client.Chain == "" || p.Client.Coin == "" {
		return chainkit.HealthStatus{
			Status:         chainkit.HealthLevelDown,
			ResponseTimeMs: 0,
			ResponseTimeUs: 0,
			Error:          "Blockcypher.com only supports Bitcoin mainnet and testnet3",
			LastChecked:    time.Now(),
			AuthValid:      authValid,
			AuthError:      authErrString(authErr),
			IsDegraded:     authValid != nil && !*authValid,
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
			Status:         chainkit.HealthLevelDown,
			ResponseTimeMs: 0,
			ResponseTimeUs: 0,
			Error:          err.Error(),
			LastChecked:    time.Now(),
			AuthValid:      authValid,
			AuthError:      authErrString(authErr),
			IsDegraded:     authValid != nil && !*authValid,
		}
	}

	resp, err := client.Do(req)
	responseDuration := time.Since(start)
	responseTimeMs := responseDuration.Milliseconds()
	responseTimeUs := responseDuration.Microseconds()

	if err != nil {
		return chainkit.HealthStatus{
			Status:         chainkit.HealthLevelDown,
			ResponseTimeMs: responseTimeMs,
			ResponseTimeUs: responseTimeUs,
			Error:          err.Error(),
			LastChecked:    time.Now(),
			AuthValid:      authValid,
			AuthError:      authErrString(authErr),
			IsDegraded:     authValid != nil && !*authValid,
		}
	}
	defer func() { _ = resp.Body.Close() }()

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

	// If auth is invalid, mark as degraded (Blockcypher has public fallback)
	isDegraded := authValid != nil && !*authValid
	if isDegraded && status == chainkit.HealthLevelHealthy {
		status = chainkit.HealthLevelDegraded
	}

	return chainkit.HealthStatus{
		Status:         status,
		ResponseTimeMs: responseTimeMs,
		ResponseTimeUs: responseTimeUs,
		HTTPStatus:     resp.StatusCode,
		Error:          errorMsg,
		LastChecked:    time.Now(),
		AuthValid:      authValid,
		AuthError:      authErrString(authErr),
		IsDegraded:     isDegraded,
	}
}

// authErrString returns the error string or empty if nil.
func authErrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// GetCapabilities returns the list of capabilities this provider supports
func (p *blockcypher) GetCapabilities() []chainkit.ProviderCapability {
	return []chainkit.ProviderCapability{
		chainkit.CapabilityAddressValidation,
		chainkit.CapabilityAPIKeyValidation,
		chainkit.CapabilityBalanceFetching,
		chainkit.CapabilityTxBroadcast,
		chainkit.CapabilityTxStatusFetching,
		chainkit.CapabilityUTXOFetching,
	}
}

// ValidateAPIKey validates the configured API token with BlockCypher's token endpoint.
// Returns nil if the token is valid, or an error containing the HTTP status if invalid.
// Updates internal auth state for use by CheckHealth().
func (p *blockcypher) ValidateAPIKey(ctx context.Context) error {
	// Helper to update auth state
	setAuthState := func(valid bool, err error) {
		p.authMu.Lock()
		p.authValid = &valid
		p.authErr = err
		p.authMu.Unlock()
	}

	if p.Client.Token == "" {
		err := fmt.Errorf("no API token configured")
		setAuthState(false, err)
		return err
	}

	url := fmt.Sprintf("https://api.blockcypher.com/v1/tokens/%s", p.Client.Token)

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		err = fmt.Errorf("create request: %w", err)
		setAuthState(false, err)
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		err = fmt.Errorf("validate API key: %w", err)
		setAuthState(false, err)
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if decodeErr := json.NewDecoder(resp.Body).Decode(&errResp); decodeErr == nil && errResp.Error != "" {
			err = fmt.Errorf("invalid API key (HTTP %d): %s", resp.StatusCode, errResp.Error)
		} else {
			err = fmt.Errorf("invalid API key (HTTP %d)", resp.StatusCode)
		}
		setAuthState(false, err)
		return err
	}

	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		err = fmt.Errorf("decode response: %w", err)
		setAuthState(false, err)
		return err
	}

	if tokenResp.Token == "" {
		err := fmt.Errorf("invalid API key: no token in response")
		setAuthState(false, err)
		return err
	}

	setAuthState(true, nil)
	return nil
}
