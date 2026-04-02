package providers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/txscript"
	"github.com/exapsy/chainkit"
	"github.com/exapsy/chainkit/bitcoin/types"
)

type MempoolProvider interface {
	// Name returns the name of the provider.
	Name() string
	chainkit.RateFetcher
	chainkit.FeeRecommender
	chainkit.TxBroadcaster
	chainkit.BalanceFetcher
	chainkit.HealthChecker
	chainkit.UTXOFetcher
	chainkit.TxStatusFetcher
}

type mempoolProvider struct {
	network    types.BitcoinNetwork
	baseURL    string
	httpClient *http.Client
}

type MempoolOptions struct {
	Network types.BitcoinNetwork
	// BaseURL is the fully resolved API endpoint for the active network
	// (e.g. "https://mempool.space/api"). Resolved from config/providers/mempool.yaml
	// by the caller.
	BaseURL string
}

func NewMempool(opts MempoolOptions) MempoolProvider {
	return &mempoolProvider{
		network:    opts.Network,
		baseURL:    opts.BaseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (m *mempoolProvider) Name() string {
	return "Mempool"
}

// mempoolPricesResponse mirrors the /v1/prices JSON shape.
type mempoolPricesResponse struct {
	Time int `json:"time"`
	USD  int `json:"USD"`
	EUR  int `json:"EUR"`
	GBP  int `json:"GBP"`
	CAD  int `json:"CAD"`
	CHF  int `json:"CHF"`
	AUD  int `json:"AUD"`
	JPY  int `json:"JPY"`
}

// fetchPrice fetches the BTC price in the given currency directly from the
// mempool.space /v1/prices endpoint (stdlib only, no external package).
func (m *mempoolProvider) fetchPrice(ctx context.Context, currency string) (float64, error) {
	url := m.baseURL + "/v1/prices"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create prices request: %w", err)
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch prices: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("failed to fetch prices: %s", resp.Status)
	}

	var prices mempoolPricesResponse
	if err := json.NewDecoder(resp.Body).Decode(&prices); err != nil {
		return 0, fmt.Errorf("failed to decode prices: %w", err)
	}

	switch currency {
	case "USD":
		return float64(prices.USD), nil
	case "EUR":
		return float64(prices.EUR), nil
	case "GBP":
		return float64(prices.GBP), nil
	case "CAD":
		return float64(prices.CAD), nil
	case "CHF":
		return float64(prices.CHF), nil
	case "AUD":
		return float64(prices.AUD), nil
	case "JPY":
		return float64(prices.JPY), nil
	default:
		return 0, fmt.Errorf("unsupported currency: %s", currency)
	}
}

func (s *mempoolProvider) GetExchangeRates(
	ctx context.Context,
	coin types.CoinTicker,
) ([]types.CoinRate, error) {

	switch coin {
	case types.CoinTickerBTC:
		price, err := s.fetchPrice(ctx, "USD")
		if err != nil {
			return nil, &types.RequestError{
				Err:     err,
				Message: "failed to fetch exchange rates",
			}
		}

		return []types.CoinRate{{
			Coin:      types.CoinTickerBTC,
			Currency:  types.CurrencyUSD,
			Source:    s.Name(),
			Network:   s.network,
			Rate:      big.NewFloat(price),
			Timestamp: time.Now(),
		}}, nil
	default:
		return nil, &types.CoinNotSupportedError{
			Coin: coin.BlockchainString(),
		}
	}
}

func (s *mempoolProvider) GetExchangeRate(
	ctx context.Context,
	coin types.CoinTicker,
	currency types.Currency,
) (*types.CoinRate, error) {

	switch coin {
	case types.CoinTickerBTC:
		price, err := s.fetchPrice(ctx, currency.String())
		if err != nil {
			return nil, &types.RequestError{
				Err:     err,
				Message: "failed to fetch exchange rate",
			}
		}

		return &types.CoinRate{
			Currency:  currency,
			Coin:      coin,
			Source:    s.Name(),
			Network:   s.network,
			Timestamp: time.Now(),
			Rate:      big.NewFloat(price),
		}, nil
	default:
		return nil, &types.CoinNotSupportedError{
			Coin: coin.BlockchainString(),
		}
	}
}

// GetBalance returns confirmed, unconfirmed, and total balance in a single network call.
func (m *mempoolProvider) GetBalance(ctx context.Context, address string) (chainkit.Balance, error) {
	url := fmt.Sprintf("%s/address/%s", m.baseURL, address)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return chainkit.Balance{}, &types.RequestError{Err: err, Message: "failed to create request"}
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return chainkit.Balance{}, &types.RequestError{Err: err, Message: "failed to fetch balance"}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return chainkit.Balance{}, &types.RequestError{
			Message: fmt.Sprintf("API returned status %d: %s", resp.StatusCode, string(body)),
		}
	}

	var result struct {
		ChainStats struct {
			FundedTxoSum int64 `json:"funded_txo_sum"`
			SpentTxoSum  int64 `json:"spent_txo_sum"`
		} `json:"chain_stats"`
		MempoolStats struct {
			FundedTxoSum int64 `json:"funded_txo_sum"`
			SpentTxoSum  int64 `json:"spent_txo_sum"`
		} `json:"mempool_stats"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return chainkit.Balance{}, &types.RequestError{Err: err, Message: "failed to decode response"}
	}

	confirmed := result.ChainStats.FundedTxoSum - result.ChainStats.SpentTxoSum
	unconfirmed := result.MempoolStats.FundedTxoSum - result.MempoolStats.SpentTxoSum
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

// GetTxFees returns fee tiers for different confirmation targets
func (m *mempoolProvider) GetTxFees(ctx context.Context) ([]types.FeeTier, error) {

	url := fmt.Sprintf("%s/v1/fees/recommended", m.baseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, &types.RequestError{Err: err, Message: "failed to create request"}
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, &types.RequestError{Err: err, Message: "failed to fetch fees"}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, &types.RequestError{Message: fmt.Sprintf("API returned status %d: %s", resp.StatusCode, string(body))}
	}

	var result struct {
		FastestFee  int64 `json:"fastestFee"`
		HalfHourFee int64 `json:"halfHourFee"`
		HourFee     int64 `json:"hourFee"`
		EconomyFee  int64 `json:"economyFee"`
		MinimumFee  int64 `json:"minimumFee"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, &types.RequestError{Err: err, Message: "failed to decode response"}
	}

	// FeeRate in sat/vB (satoshis per virtual byte)
	return []types.FeeTier{
		{FeeRate: uint64(result.FastestFee), TargetBlock: 1},
		{FeeRate: uint64(result.HalfHourFee), TargetBlock: 3},
		{FeeRate: uint64(result.HourFee), TargetBlock: 6},
		{FeeRate: uint64(result.EconomyFee), TargetBlock: 144},
		{FeeRate: uint64(result.MinimumFee), TargetBlock: 1008},
	}, nil
}

// GetTxFee returns the fee tier that best matches the requested priority.
func (m *mempoolProvider) GetTxFee(ctx context.Context, priority types.FeePriority) (types.FeeTier, error) {
	fees, err := m.GetTxFees(ctx)
	if err != nil {
		return types.FeeTier{}, err
	}

	return priority.SelectClosest(fees), nil
}

// PushTx broadcasts a signed transaction to the network
func (m *mempoolProvider) PushTx(ctx context.Context, rawTx []byte) (string, error) {

	// Convert raw bytes to hex string
	hexTx := hex.EncodeToString(rawTx)

	url := fmt.Sprintf("%s/tx", m.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(hexTx))
	if err != nil {
		return "", &types.RequestError{Err: err, Message: "failed to create request"}
	}

	// Mempool expects plain text, not JSON
	req.Header.Set("Content-Type", "text/plain")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", &types.RequestError{Err: err, Message: "failed to broadcast transaction"}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return "", &types.RequestError{
			Message: fmt.Sprintf("broadcast failed (status %d): %s", resp.StatusCode, string(body)),
		}
	}

	// Mempool returns the txid as plain text
	txid := strings.TrimSpace(string(body))
	return txid, nil
}

// CheckHealth performs a health check on the mempool.space API
func (m *mempoolProvider) CheckHealth(ctx context.Context) chainkit.HealthStatus {
	start := time.Now()

	url := fmt.Sprintf("%s/blocks/tip/height", m.baseURL)
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

	resp, err := m.httpClient.Do(req)
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

	result := chainkit.HealthStatus{
		Status:         status,
		ResponseTimeMs: responseTimeMs,
		ResponseTimeUs: responseTimeUs,
		HTTPStatus:     resp.StatusCode,
		Error:          errorMsg,
		LastChecked:    time.Now(),
	}

	return result
}

// GetCapabilities returns the list of capabilities this provider supports
func (m *mempoolProvider) GetCapabilities() []chainkit.ProviderCapability {
	return []chainkit.ProviderCapability{
		chainkit.CapabilityBalanceFetching,
		chainkit.CapabilityFeeRecommending,
		chainkit.CapabilityRateFetching,
		chainkit.CapabilityTxBroadcast,
		chainkit.CapabilityTxStatusFetching,
		chainkit.CapabilityUTXOFetching,
	}
}

// GetUTXOs fetches unspent transaction outputs for a given address
func (m *mempoolProvider) GetUTXOs(ctx context.Context, address string) ([]types.UTXO, error) {

	url := fmt.Sprintf("%s/address/%s/utxo", m.baseURL, address)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, &types.RequestError{Err: err, Message: "failed to create request"}
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, &types.RequestError{Err: err, Message: "failed to fetch UTXOs"}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, &types.RequestError{
			Message: fmt.Sprintf("API returned status %d: %s", resp.StatusCode, string(body)),
		}
	}

	// Decode the address to get the ScriptPubKey
	scriptPubKey, err := m.addressToScriptPubKey(address)
	if err != nil {
		return nil, &types.RequestError{Err: err, Message: "failed to decode address"}
	}

	// Mempool.space returns an array of UTXO objects
	var apiUTXOs []struct {
		TxID   string `json:"txid"`
		Vout   uint32 `json:"vout"`
		Status struct {
			Confirmed   bool  `json:"confirmed"`
			BlockHeight int32 `json:"block_height"`
			BlockTime   int64 `json:"block_time"`
		} `json:"status"`
		Value int64 `json:"value"` // Amount in satoshis
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiUTXOs); err != nil {
		return nil, &types.RequestError{Err: err, Message: "failed to decode response"}
	}

	utxos := make([]types.UTXO, 0, len(apiUTXOs))
	for _, apiUtxo := range apiUTXOs {
		// Calculate confirmations based on current tip height if needed
		confirmations := int64(0)
		if apiUtxo.Status.Confirmed && apiUtxo.Status.BlockHeight > 0 {
			// You might want to fetch the current block height for accurate confirmations
			// For now, just mark as confirmed
			confirmations = 1
		}

		utxo := types.UTXO{
			TxHash:        apiUtxo.TxID,
			Vout:          apiUtxo.Vout,
			Amount:        apiUtxo.Value,
			Address:       address,
			Confirmed:     apiUtxo.Status.Confirmed,
			Confirmations: confirmations,
			Spendable:     true, // Mempool only returns unspent outputs
			BlockHeight:   apiUtxo.Status.BlockHeight,
			ScriptPubKey:  scriptPubKey,
		}

		utxos = append(utxos, utxo)
	}

	return utxos, nil
}

// addressToScriptPubKey converts a Bitcoin address to its corresponding ScriptPubKey
func (m *mempoolProvider) addressToScriptPubKey(address string) ([]byte, error) {
	// Get the network parameters
	params, err := m.network.ChaincfgNetwork()
	if err != nil {
		return nil, fmt.Errorf("failed to get network params: %w", err)
	}

	// Decode the address using btcutil
	addr, err := btcutil.DecodeAddress(address, params)
	if err != nil {
		return nil, fmt.Errorf("failed to decode address: %w", err)
	}

	// Generate the ScriptPubKey based on address type
	script, err := txscript.PayToAddrScript(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to generate script: %w", err)
	}

	return script, nil
}

// GetTxStatus returns the confirmation status of a transaction
func (m *mempoolProvider) GetTxStatus(ctx context.Context, txID string) (*chainkit.TxConfirmationStatus, error) {

	// Mempool.space API endpoint for transaction status
	url := fmt.Sprintf("%s/tx/%s", m.baseURL, txID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, &types.RequestError{Err: err, Message: "failed to create request"}
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, &types.RequestError{Err: err, Message: "failed to fetch transaction"}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &types.RequestError{Message: "transaction not found"}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &types.RequestError{Message: fmt.Sprintf("unexpected status code: %d", resp.StatusCode)}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &types.RequestError{Err: err, Message: "failed to read response body"}
	}

	// Parse the transaction response
	var txResp struct {
		TxID   string `json:"txid"`
		Status struct {
			Confirmed   bool   `json:"confirmed"`
			BlockHeight int64  `json:"block_height"`
			BlockHash   string `json:"block_hash"`
			BlockTime   int64  `json:"block_time"`
		} `json:"status"`
	}

	if err := json.Unmarshal(body, &txResp); err != nil {
		return nil, &types.RequestError{Err: err, Message: "failed to decode response"}
	}

	// Calculate confirmations if confirmed
	confirmations := 0
	if txResp.Status.Confirmed && txResp.Status.BlockHeight > 0 {
		// Fetch current block height to calculate confirmations
		tipHeight, err := m.getCurrentBlockHeight(ctx)
		if err == nil && tipHeight > 0 {
			confirmations = int(tipHeight - txResp.Status.BlockHeight + 1)
		} else {
			// If we can't get tip height, just mark as 1 confirmation
			confirmations = 1
		}
	}

	return &chainkit.TxConfirmationStatus{
		Confirmed:     txResp.Status.Confirmed,
		Confirmations: confirmations,
		BlockHeight:   txResp.Status.BlockHeight,
		BlockHash:     txResp.Status.BlockHash,
	}, nil
}

// getCurrentBlockHeight fetches the current blockchain tip height
func (m *mempoolProvider) getCurrentBlockHeight(ctx context.Context) (int64, error) {
	url := fmt.Sprintf("%s/blocks/tip/height", m.baseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var height int64
	if err := json.Unmarshal(body, &height); err != nil {
		return 0, err
	}

	return height, nil
}
