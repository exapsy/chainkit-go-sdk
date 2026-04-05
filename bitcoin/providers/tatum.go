package providers

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/exapsy/chainkit"
	"github.com/exapsy/chainkit/bitcoin/types"
)

// TatumProvider talks to Tatum's Bitcoin gateway nodes via JSON-RPC.
// Supports broadcasting transactions, fetching transaction status, and fee estimation.
// Does NOT support address balance or UTXO queries (no indexer on gateway nodes).
type TatumProvider interface {
	chainkit.BlockchainBaseProvider
	chainkit.TxBroadcaster
	chainkit.TxStatusFetcher
	chainkit.HealthChecker
	chainkit.FeeRecommender
	chainkit.APIKeyValidator
}

type tatumProvider struct {
	network    types.BitcoinNetwork
	baseURL    string
	apiKey     string
	httpClient *http.Client

	// Auth state (updated by ValidateAPIKey, read by CheckHealth)
	authMu    sync.RWMutex
	authValid *bool
	authErr   error
}

// NewTatum creates a TatumProvider that communicates with a Tatum gateway node
// via JSON-RPC. The API key is sent as the x-api-key header.
//
// Gateway URLs by network:
//   - mainnet:  https://bitcoin-mainnet.gateway.tatum.io
//   - testnet3: https://bitcoin-testnet.gateway.tatum.io
//   - testnet4: https://bitcoin-testnet4.gateway.tatum.io
func NewTatum(network types.BitcoinNetwork, baseURL string, apiKey string) TatumProvider {
	return &tatumProvider{
		network:    network,
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *tatumProvider) Name() string {
	return "Tatum"
}

type tatumRPCRequest struct {
	Method string      `json:"method"`
	Params interface{} `json:"params"`
	ID     int         `json:"id"`
}

type tatumRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	ID int `json:"id"`
}

func (t *tatumProvider) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	body, err := json.Marshal(tatumRPCRequest{Method: method, Params: params, ID: 1})
	if err != nil {
		return nil, fmt.Errorf("marshal RPC request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if t.apiKey != "" {
		req.Header.Set("x-api-key", t.apiKey)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp tatumRPCResponse
	if err = json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("parse RPC response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

// PushTx broadcasts a signed transaction via sendrawtransaction.
func (t *tatumProvider) PushTx(ctx context.Context, rawTx []byte) (string, error) {
	result, err := t.call(ctx, "sendrawtransaction", []string{hex.EncodeToString(rawTx)})
	if err != nil {
		return "", fmt.Errorf("broadcast transaction: %w", err)
	}

	var txID string
	if err = json.Unmarshal(result, &txID); err != nil {
		return "", fmt.Errorf("parse txid: %w", err)
	}

	return txID, nil
}

// GetTxStatus returns the confirmation status of a transaction via getrawtransaction.
func (t *tatumProvider) GetTxStatus(ctx context.Context, txID string) (*chainkit.TxConfirmationStatus, error) {
	result, err := t.call(ctx, "getrawtransaction", []interface{}{txID, true})
	if err != nil {
		return nil, fmt.Errorf("fetch tx status %s: %w", txID, err)
	}

	var tx struct {
		Confirmations *int64  `json:"confirmations"`
		BlockHash     *string `json:"blockhash"`
		BlockHeight   *int64  `json:"blockheight"`
	}
	if err = json.Unmarshal(result, &tx); err != nil {
		return nil, fmt.Errorf("parse tx status response: %w", err)
	}

	if tx.Confirmations == nil || *tx.Confirmations == 0 {
		return &chainkit.TxConfirmationStatus{
			Confirmed:     false,
			Confirmations: 0,
		}, nil
	}

	blockHash := ""
	if tx.BlockHash != nil {
		blockHash = *tx.BlockHash
	}
	blockHeight := int64(0)
	if tx.BlockHeight != nil {
		blockHeight = *tx.BlockHeight
	}

	return &chainkit.TxConfirmationStatus{
		Confirmed:     true,
		Confirmations: int(*tx.Confirmations),
		BlockHash:     blockHash,
		BlockHeight:   blockHeight,
	}, nil
}

// CheckHealth performs a health check via getblockchaininfo.
func (t *tatumProvider) CheckHealth(ctx context.Context) chainkit.HealthStatus {
	start := time.Now()

	// Read current auth state
	t.authMu.RLock()
	authValid := t.authValid
	authErr := t.authErr
	t.authMu.RUnlock()

	result, err := t.call(ctx, "getblockchaininfo", []interface{}{})
	dur := time.Since(start)
	ms := dur.Milliseconds()
	us := dur.Microseconds()

	// Build auth error string if present
	var authErrStr string
	if authErr != nil {
		authErrStr = authErr.Error()
	}

	// Tatum requires auth - if auth is invalid, provider is down
	if authValid != nil && !*authValid {
		return chainkit.HealthStatus{
			Status:         chainkit.HealthLevelDown,
			ResponseTimeMs: ms,
			ResponseTimeUs: us,
			Error:          "authentication failed",
			LastChecked:    time.Now(),
			AuthValid:      authValid,
			AuthError:      authErrStr,
			IsDegraded:     true,
		}
	}

	if err != nil {
		return chainkit.HealthStatus{
			Status:         chainkit.HealthLevelDown,
			ResponseTimeMs: ms,
			ResponseTimeUs: us,
			Error:          err.Error(),
			LastChecked:    time.Now(),
			AuthValid:      authValid,
			AuthError:      authErrStr,
		}
	}

	var info struct {
		Blocks int64 `json:"blocks"`
	}
	if err = json.Unmarshal(result, &info); err != nil || info.Blocks == 0 {
		return chainkit.HealthStatus{
			Status:         chainkit.HealthLevelDegraded,
			ResponseTimeMs: ms,
			ResponseTimeUs: us,
			Error:          "unexpected getblockchaininfo response",
			LastChecked:    time.Now(),
			AuthValid:      authValid,
			AuthError:      authErrStr,
		}
	}

	status := chainkit.HealthLevelHealthy
	errMsg := ""
	if ms > 2000 {
		status = chainkit.HealthLevelDegraded
		errMsg = "slow response"
	}

	return chainkit.HealthStatus{
		Status:         status,
		ResponseTimeMs: ms,
		ResponseTimeUs: us,
		HTTPStatus:     http.StatusOK,
		Error:          errMsg,
		LastChecked:    time.Now(),
		AuthValid:      authValid,
		AuthError:      authErrStr,
	}
}

// GetCapabilities returns the capabilities this provider supports.
func (t *tatumProvider) GetCapabilities() []chainkit.ProviderCapability {
	return []chainkit.ProviderCapability{
		chainkit.CapabilityAPIKeyValidation,
		chainkit.CapabilityTxBroadcast,
		chainkit.CapabilityTxStatusFetching,
		chainkit.CapabilityFeeRecommending,
	}
}

// ValidateAPIKey validates the API key by making a getblockcount RPC call.
// Returns nil if valid, or an error if the credentials are invalid.
// Updates internal auth state for use by CheckHealth.
func (t *tatumProvider) ValidateAPIKey(ctx context.Context) error {
	body, err := json.Marshal(tatumRPCRequest{Method: "getblockcount", Params: []interface{}{}, ID: 1})
	if err != nil {
		return fmt.Errorf("marshal RPC request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if t.apiKey != "" {
		req.Header.Set("x-api-key", t.apiKey)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("validate API key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		authErr := fmt.Errorf("invalid API key or credentials (HTTP %d)", resp.StatusCode)
		t.setAuthState(false, authErr)
		return authErr
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}

	t.setAuthState(true, nil)
	return nil
}

// setAuthState updates the internal auth state in a thread-safe manner.
func (t *tatumProvider) setAuthState(valid bool, err error) {
	t.authMu.Lock()
	defer t.authMu.Unlock()
	t.authValid = &valid
	t.authErr = err
}

// estimateFeeForTarget calls estimatesmartfee for the given confirmation target
// and returns a FeeTier with FeeRate in sat/vByte.
func (t *tatumProvider) estimateFeeForTarget(ctx context.Context, targetBlocks int) (types.FeeTier, error) {
	result, err := t.call(ctx, "estimatesmartfee", []interface{}{targetBlocks})
	if err != nil {
		return types.FeeTier{}, fmt.Errorf("estimate fee for %d blocks: %w", targetBlocks, err)
	}

	var resp struct {
		FeeRate float64  `json:"feerate"` // BTC/kB; negative when node lacks data
		Blocks  int      `json:"blocks"`  // actual target the node used
		Errors  []string `json:"errors"`  // non-fatal warnings from the node
	}
	if err = json.Unmarshal(result, &resp); err != nil {
		return types.FeeTier{}, fmt.Errorf("parse fee estimate response: %w", err)
	}

	if len(resp.Errors) > 0 {
		return types.FeeTier{}, fmt.Errorf("estimatesmartfee: %s", strings.Join(resp.Errors, "; "))
	}

	if resp.FeeRate <= 0 {
		return types.FeeTier{}, fmt.Errorf("no fee data for target %d blocks", targetBlocks)
	}

	// Convert BTC/kB → sat/vByte: ×1e8 (BTC→sat) ÷1000 (kB→B)
	satPerVByte := uint64(math.Round(resp.FeeRate * 1e8 / 1000))
	if satPerVByte < 1 {
		satPerVByte = 1
	}

	blocks := resp.Blocks
	if blocks == 0 {
		blocks = targetBlocks
	}

	return types.FeeTier{
		FeeRate:     satPerVByte,
		TargetBlock: blocks,
	}, nil
}

// GetTxFees returns fee estimates for all standard priority levels.
// Priorities that the node cannot estimate (insufficient data) are silently omitted.
func (t *tatumProvider) GetTxFees(ctx context.Context) ([]types.FeeTier, error) {
	priorities := []types.FeePriority{
		types.FeePriorityFastest,
		types.FeePriorityFast,
		types.FeePriorityMedium,
		types.FeePrioritySlow,
		types.FeePriorityMinimum,
	}

	var tiers []types.FeeTier
	for _, p := range priorities {
		tier, err := t.estimateFeeForTarget(ctx, p.TargetBlock())
		if err != nil {
			continue
		}
		tiers = append(tiers, tier)
	}

	if len(tiers) == 0 {
		return nil, fmt.Errorf("no fee estimates available: node may lack sufficient data")
	}

	return tiers, nil
}

// GetTxFee returns the fee estimate for the requested priority level.
func (t *tatumProvider) GetTxFee(ctx context.Context, priority types.FeePriority) (types.FeeTier, error) {
	return t.estimateFeeForTarget(ctx, priority.TargetBlock())
}
