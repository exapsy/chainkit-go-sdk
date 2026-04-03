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
	"strconv"
	"strings"
	"time"

	"github.com/exapsy/chainkit"
	"github.com/exapsy/chainkit/bitcoin/types"
)

const (
	tatumDefaultBaseURL = "https://api.tatum.io/v3/bitcoin"
)

type TatumProvider interface {
	chainkit.BlockchainBaseProvider
	chainkit.BalanceFetcher
	chainkit.UTXOFetcher
	chainkit.TxBroadcaster
	chainkit.TxStatusFetcher
	chainkit.HealthChecker
}

type tatum struct {
	network    types.BitcoinNetwork
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewTatum creates a TatumProvider for the given network.
// If baseURL is empty it defaults to the public Tatum V3 Bitcoin API.
func NewTatum(network types.BitcoinNetwork, baseURL string, apiKey string) TatumProvider {
	if baseURL == "" {
		baseURL = tatumDefaultBaseURL
	}
	return &tatum{
		network:    network,
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *tatum) Name() string {
	return "Tatum"
}

// callAPI is a shared helper for all Tatum API calls.
// It sets the x-api-key header, optionally sets Content-Type for POST/PUT,
// and returns the raw response body on HTTP 200. Any other status is an error.
func (t *tatum) callAPI(ctx context.Context, method string, endpoint string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, t.baseURL+endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("x-api-key", t.apiKey)
	if body != nil && (method == http.MethodPost || method == http.MethodPut) {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return respBody, nil
}

// parseBTCToSatoshis converts a BTC decimal string (e.g. "0.00123") to satoshis.
func parseBTCToSatoshis(s string) (int64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid BTC value %q: %w", s, err)
	}
	return int64(math.Round(v * 1e8)), nil
}

// GetBalance fetches confirmed and unconfirmed balance for the given address.
// Tatum returns BTC decimal strings for the balance endpoint; they are converted to satoshis.
func (t *tatum) GetBalance(ctx context.Context, address string) (chainkit.Balance, error) {
	body, err := t.callAPI(ctx, http.MethodGet, "/address/balance/"+address, nil)
	if err != nil {
		return chainkit.Balance{}, fmt.Errorf("failed to fetch balance from Tatum: %w", err)
	}

	var resp struct {
		Incoming        string `json:"incoming"`
		Outgoing        string `json:"outgoing"`
		IncomingPending string `json:"incomingPending"`
		OutgoingPending string `json:"outgoingPending"`
	}
	if err = json.Unmarshal(body, &resp); err != nil {
		return chainkit.Balance{}, fmt.Errorf("failed to parse balance response: %w", err)
	}

	incoming, err := parseBTCToSatoshis(resp.Incoming)
	if err != nil {
		return chainkit.Balance{}, fmt.Errorf("failed to parse incoming balance: %w", err)
	}
	outgoing, err := parseBTCToSatoshis(resp.Outgoing)
	if err != nil {
		return chainkit.Balance{}, fmt.Errorf("failed to parse outgoing balance: %w", err)
	}
	incomingPending, err := parseBTCToSatoshis(resp.IncomingPending)
	if err != nil {
		return chainkit.Balance{}, fmt.Errorf("failed to parse incomingPending balance: %w", err)
	}
	outgoingPending, err := parseBTCToSatoshis(resp.OutgoingPending)
	if err != nil {
		return chainkit.Balance{}, fmt.Errorf("failed to parse outgoingPending balance: %w", err)
	}

	confirmed := incoming - outgoing
	if confirmed < 0 {
		confirmed = 0
	}
	unconfirmed := incomingPending - outgoingPending
	if unconfirmed < 0 {
		unconfirmed = 0
	}

	return chainkit.Balance{
		Confirmed:   uint64(confirmed),
		Unconfirmed: uint64(unconfirmed),
		Total:       uint64(confirmed + unconfirmed),
	}, nil
}

// GetUTXOs returns unspent transaction outputs for the given address.
//
// Tatum has no single "list all UTXOs by address" endpoint. This method
// fetches the first page (50) of transactions for the address and probes
// each relevant output via GET /utxo/{txid}/{index}. Tatum returns HTTP 403
// for spent outputs ("btc.tx.hash.index.spent") — those are silently skipped.
// Only the first 50 transactions are considered; addresses with very high
// transaction volume may not see all UTXOs.
func (t *tatum) GetUTXOs(ctx context.Context, address string) ([]types.UTXO, error) {
	txBody, err := t.callAPI(ctx, http.MethodGet, "/transaction/address/"+address+"?pageSize=50", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch transactions from Tatum: %w", err)
	}

	// Actual response shape (verified against live API):
	// outputs[].value  → satoshis (integer)
	// outputs[].script → scriptPubKey hex
	// outputs[].address→ recipient address
	// Array index = vout index
	var txList []struct {
		Hash    string `json:"hash"`
		Outputs []struct {
			Address string `json:"address"`
			Value   int64  `json:"value"`
			Script  string `json:"script"`
		} `json:"outputs"`
	}
	if err = json.Unmarshal(txBody, &txList); err != nil {
		return nil, fmt.Errorf("failed to parse transaction list: %w", err)
	}

	tipHeight, _ := t.getCurrentBlockHeight(ctx) // best effort for confirmations calc

	var utxos []types.UTXO

	for _, tx := range txList {
		for voutIdx, out := range tx.Outputs {
			if out.Address != address {
				continue
			}

			utxoBody, err := t.callAPI(
				ctx,
				http.MethodGet,
				fmt.Sprintf("/utxo/%s/%d", tx.Hash, voutIdx),
				nil,
			)
			if err != nil {
				// Tatum returns 403 ("btc.tx.hash.index.spent") for spent outputs
				// and 403 ("btc.tx.not.found") or 404 when the output doesn't exist.
				if strings.Contains(err.Error(), "API returned status 403") ||
					strings.Contains(err.Error(), "API returned status 404") {
					continue
				}
				return nil, fmt.Errorf("failed to fetch UTXO %s:%d: %w", tx.Hash, voutIdx, err)
			}

			// Actual UTXO response shape (verified against live API):
			// value  → satoshis (integer)
			// script → scriptPubKey hex
			// hash   → txid
			// index  → vout output index
			// height → block height (no confirmations field)
			var u struct {
				Value   int64  `json:"value"`
				Address string `json:"address"`
				Script  string `json:"script"`
				Hash    string `json:"hash"`
				Index   uint32 `json:"index"`
				Height  int32  `json:"height"`
			}
			if err = json.Unmarshal(utxoBody, &u); err != nil {
				return nil, fmt.Errorf("failed to parse UTXO response: %w", err)
			}

			scriptBytes, _ := hex.DecodeString(u.Script)

			confirmed := u.Height > 0
			confirmations := int64(0)
			if confirmed && tipHeight > 0 {
				confirmations = tipHeight - int64(u.Height) + 1
			} else if confirmed {
				confirmations = 1
			}

			utxos = append(utxos, types.UTXO{
				TxHash:        u.Hash,
				Vout:          u.Index,
				Amount:        u.Value,
				Address:       u.Address,
				ScriptPubKey:  scriptBytes,
				Confirmed:     confirmed,
				Confirmations: confirmations,
				Spendable:     true,
				BlockHeight:   u.Height,
			})
		}
	}

	return utxos, nil
}

// PushTx broadcasts a signed transaction to the Bitcoin network via Tatum's broadcast endpoint.
func (t *tatum) PushTx(ctx context.Context, rawTx []byte) (string, error) {
	reqBody, err := json.Marshal(struct {
		TxData string `json:"txData"`
	}{
		TxData: hex.EncodeToString(rawTx),
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal broadcast request: %w", err)
	}

	body, err := t.callAPI(ctx, http.MethodPost, "/broadcast", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to broadcast transaction via Tatum: %w", err)
	}

	var resp struct {
		TxID   string `json:"txId"`
		Failed bool   `json:"failed"`
	}
	if err = json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("failed to parse broadcast response: %w", err)
	}

	if resp.Failed {
		return "", fmt.Errorf("transaction broadcast failed")
	}

	return resp.TxID, nil
}

// GetTxStatus returns the confirmation status of a transaction.
// Tatum's transaction response has no confirmations field; they are computed
// from the current block height. If the tip-height fetch fails the call still
// succeeds and reports Confirmations: 1 for confirmed transactions.
func (t *tatum) GetTxStatus(ctx context.Context, txID string) (*chainkit.TxConfirmationStatus, error) {
	body, err := t.callAPI(ctx, http.MethodGet, "/transaction/"+txID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tx status from Tatum: %w", err)
	}

	var tx struct {
		BlockNumber *int64  `json:"blockNumber"`
		Block       *string `json:"block"`
	}
	if err = json.Unmarshal(body, &tx); err != nil {
		return nil, fmt.Errorf("failed to parse tx status response: %w", err)
	}

	if tx.BlockNumber == nil || *tx.BlockNumber == 0 {
		return &chainkit.TxConfirmationStatus{
			Confirmed:     false,
			Confirmations: 0,
		}, nil
	}

	blockHash := ""
	if tx.Block != nil {
		blockHash = *tx.Block
	}

	confirmations := 1
	if tipHeight, err := t.getCurrentBlockHeight(ctx); err == nil && tipHeight > 0 {
		confirmations = int(tipHeight - *tx.BlockNumber + 1)
	}

	return &chainkit.TxConfirmationStatus{
		Confirmed:     true,
		Confirmations: confirmations,
		BlockHeight:   *tx.BlockNumber,
		BlockHash:     blockHash,
	}, nil
}

// getCurrentBlockHeight fetches the current Bitcoin chain tip height from Tatum.
// It uses GET /info which returns {"blocks": <height>, ...}.
func (t *tatum) getCurrentBlockHeight(ctx context.Context) (int64, error) {
	body, err := t.callAPI(ctx, http.MethodGet, "/info", nil)
	if err != nil {
		return 0, err
	}

	var info struct {
		Blocks int64 `json:"blocks"`
	}
	if err = json.Unmarshal(body, &info); err != nil {
		return 0, fmt.Errorf("failed to parse blockchain info: %w", err)
	}

	return info.Blocks, nil
}

// CheckHealth performs a health check against the Tatum Bitcoin API.
func (t *tatum) CheckHealth(ctx context.Context) chainkit.HealthStatus {
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+"/info", nil)
	if err != nil {
		return chainkit.HealthStatus{
			Status:      chainkit.HealthLevelDown,
			Error:       err.Error(),
			LastChecked: time.Now(),
		}
	}

	req.Header.Set("x-api-key", t.apiKey)

	resp, err := t.httpClient.Do(req)
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

// GetCapabilities returns the list of capabilities this provider supports.
func (t *tatum) GetCapabilities() []chainkit.ProviderCapability {
	return []chainkit.ProviderCapability{
		chainkit.CapabilityBalanceFetching,
		chainkit.CapabilityTxBroadcast,
		chainkit.CapabilityTxStatusFetching,
		chainkit.CapabilityUTXOFetching,
	}
}
