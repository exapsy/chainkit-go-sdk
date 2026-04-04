package providers

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/exapsy/chainkit"
	"github.com/exapsy/chainkit/bitcoin/types"
)

// RPCProvider talks to a Bitcoin Core-compatible JSON-RPC node.
// It supports broadcasting transactions, fetching transaction status,
// and health checks. It does NOT support address balance or UTXO
// queries (those require an indexer, which standard nodes do not have).
//
// Use this for providers that expose a raw Bitcoin node — e.g. Tatum's
// gateway nodes (bitcoin-mainnet.gateway.tatum.io, etc.).
type RPCProvider interface {
	chainkit.BlockchainBaseProvider
	chainkit.TxBroadcaster
	chainkit.TxStatusFetcher
	chainkit.HealthChecker
}

type rpcProvider struct {
	network    types.BitcoinNetwork
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewRPC creates an RPCProvider that communicates with a Bitcoin Core
// JSON-RPC node. apiKey is sent as the x-api-key header; leave empty
// if the node requires no authentication.
func NewRPC(network types.BitcoinNetwork, baseURL string, apiKey string) RPCProvider {
	return &rpcProvider{
		network:    network,
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (r *rpcProvider) Name() string {
	return "Tatum"
}

type rpcRequest struct {
	Method string      `json:"method"`
	Params interface{} `json:"params"`
	ID     int         `json:"id"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	ID int `json:"id"`
}

func (r *rpcProvider) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	body, err := json.Marshal(rpcRequest{Method: method, Params: params, ID: 1})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal RPC request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("x-api-key", r.apiKey)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("node returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp rpcResponse
	if err = json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("failed to parse RPC response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

// PushTx broadcasts a signed transaction via sendrawtransaction.
func (r *rpcProvider) PushTx(ctx context.Context, rawTx []byte) (string, error) {
	result, err := r.call(ctx, "sendrawtransaction", []string{hex.EncodeToString(rawTx)})
	if err != nil {
		return "", fmt.Errorf("failed to broadcast transaction: %w", err)
	}

	var txID string
	if err = json.Unmarshal(result, &txID); err != nil {
		return "", fmt.Errorf("failed to parse txid from broadcast response: %w", err)
	}

	return txID, nil
}

// GetTxStatus returns the confirmation status of a transaction via getrawtransaction.
func (r *rpcProvider) GetTxStatus(ctx context.Context, txID string) (*chainkit.TxConfirmationStatus, error) {
	result, err := r.call(ctx, "getrawtransaction", []interface{}{txID, true})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tx status: %w", err)
	}

	var tx struct {
		Confirmations *int64  `json:"confirmations"`
		BlockHash     *string `json:"blockhash"`
		BlockHeight   *int64  `json:"blockheight"`
	}
	if err = json.Unmarshal(result, &tx); err != nil {
		return nil, fmt.Errorf("failed to parse tx status response: %w", err)
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
func (r *rpcProvider) CheckHealth(ctx context.Context) chainkit.HealthStatus {
	start := time.Now()

	result, err := r.call(ctx, "getblockchaininfo", []interface{}{})
	dur := time.Since(start)
	ms := dur.Milliseconds()
	us := dur.Microseconds()

	if err != nil {
		return chainkit.HealthStatus{
			Status:         chainkit.HealthLevelDown,
			ResponseTimeMs: ms,
			ResponseTimeUs: us,
			Error:          err.Error(),
			LastChecked:    time.Now(),
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
	}
}

// GetCapabilities returns the capabilities this provider supports.
func (r *rpcProvider) GetCapabilities() []chainkit.ProviderCapability {
	return []chainkit.ProviderCapability{
		chainkit.CapabilityTxBroadcast,
		chainkit.CapabilityTxStatusFetching,
	}
}
