package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/exapsy/chainkit"
	"github.com/exapsy/chainkit/bitcoin/types"
)

const (
	BlockstreamDefaultConfirmationMonitorInterval             = time.Minute
	BlockstreamDefaultConfirmationMonitorFailedTimesThreshold = 3
)

type BlockstreamProvider interface {
	chainkit.BlockchainBaseProvider
	chainkit.UTXOFetcher
	chainkit.BalanceFetcher
	chainkit.AddressValidator
	chainkit.FeeFetcher
}

type blockstream struct {
	clientID     string
	clientSecret string

	tokenMu                   sync.RWMutex
	accessToken               string
	accessTokenExpirationTime time.Time

	baseURL  string
	loginURL string
}

type BlockstreamOptions struct {
	ClientID                                string
	ClientSecret                            string
	ConfirmationMonitorInterval             time.Duration
	ConfirmationMonitorFailedTimesThreshold int

	// BaseURL is the API base URL for the active network, resolved from
	// config/providers/blockstream.yaml by the caller.
	BaseURL string
	// LoginURL is the OAuth login URL for the active network, resolved from
	// config/providers/blockstream.yaml by the caller.
	LoginURL string
}

// NewBlockstream initializes a new BlockstreamProvider instance.
// BaseURL and LoginURL must be supplied by the caller, resolved from
// config/providers/blockstream.yaml for the active Bitcoin network.
// Returns an error if required fields are missing.
func NewBlockstream(options BlockstreamOptions) (BlockstreamProvider, error) {
	if options.ClientID == "" || options.ClientSecret == "" {
		return nil, errors.New("Blockstream: API client ID and secret are required")
	}

	if options.BaseURL == "" {
		return nil, errors.New("Blockstream: BaseURL is required; ensure config/providers/blockstream.yaml has an entry for the active network")
	}

	if options.LoginURL == "" {
		return nil, errors.New("Blockstream: LoginURL is required; ensure config/providers/blockstream.yaml has an entry for the active network")
	}

	if options.ConfirmationMonitorInterval == 0 {
		options.ConfirmationMonitorInterval = BlockstreamDefaultConfirmationMonitorInterval
	}

	if options.ConfirmationMonitorFailedTimesThreshold < 0 {
		options.ConfirmationMonitorFailedTimesThreshold = BlockstreamDefaultConfirmationMonitorFailedTimesThreshold
	}

	return &blockstream{
		clientID:     options.ClientID,
		clientSecret: options.ClientSecret,
		baseURL:      options.BaseURL,
		loginURL:     options.LoginURL,
	}, nil
}

func (p *blockstream) Name() string {
	return "Blockstream"
}

func (p *blockstream) FetchUTXOs(ctx context.Context, address string) ([]types.UTXO, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	// GET /address/:address/utxo
	//
	// Get the list of unspent transaction outputs associated with the address/scripthash.
	//
	// Available fields: txid, vout, value and status (with the status of the funding tx).
	//
	// Elements-based chains have a valuecommitment field that may appear in place of value, plus the following additional fields: asset/assetcommitment, nonce/noncecommitment, surjection_proof and range_proof.
	//
	endpoint := fmt.Sprintf("/address/%s/utxo", address)

	body, err := p.callAPI("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch UTXOs from Blockstream: %w", err)
	}

	var utxoResponses []struct {
		TxID  string `json:"txid"`
		Vout  uint32 `json:"vout"`
		Value uint64 `json:"value"`
	}

	err = json.Unmarshal(body, &utxoResponses)
	if err != nil {
		return nil, fmt.Errorf("failed to parse UTXO response: %w", err)
	}

	var utxos []types.UTXO
	for _, utxoResp := range utxoResponses {
		utxos = append(utxos, types.UTXO{
			TxHash: utxoResp.TxID,
			Vout:   utxoResp.Vout,
			Amount: int64(utxoResp.Value),
		})
	}

	return utxos, nil
}

func (p *blockstream) GetBalance(ctx context.Context, address string, options *chainkit.GetBalanceOptions) (uint64, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	utxos, err := p.FetchUTXOs(ctx, address)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch UTXOs: %w", err)
	}

	var balance uint64
	for _, utxo := range utxos {
		balance += uint64(utxo.Amount)
	}

	return balance, nil
}

func (p *blockstream) GetConfirmedBalance(ctx context.Context, address string) (uint64, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	utxos, err := p.FetchUTXOs(ctx, address)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch UTXOs: %w", err)
	}

	var balance uint64
	for _, utxo := range utxos {
		// Get transaction status to check if confirmed
		txStatus, err := p.getTxStatus(utxo.TxHash)
		if err != nil {
			return 0, fmt.Errorf("failed to get transaction status: %w", err)
		}

		if txStatus.Confirmed {
			balance += uint64(utxo.Amount)
		}
	}

	return balance, nil
}

func (p *blockstream) GetUnconfirmedBalance(ctx context.Context, address string) (uint64, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	utxos, err := p.FetchUTXOs(ctx, address)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch UTXOs: %w", err)
	}

	var balance uint64
	for _, utxo := range utxos {
		// Get transaction status to check if confirmed
		txStatus, err := p.getTxStatus(utxo.TxHash)
		if err != nil {
			return 0, fmt.Errorf("failed to get transaction status: %w", err)
		}

		if !txStatus.Confirmed {
			balance += uint64(utxo.Amount)
		}
	}

	return balance, nil
}

func (p *blockstream) ValidateAddress(ctx context.Context, address string) (bool, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	// Use Blockstream's address endpoint to validate the address
	// If the address is invalid, Blockstream will return a 400 error
	endpoint := fmt.Sprintf("%s/address/%s", p.baseURL, address)

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to validate address with Blockstream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 400 {
		// Invalid address format
		return false, nil
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("blockstream API error: %d - %s", resp.StatusCode, string(body))
	}

	// If we get a 200 response, the address is valid
	return true, nil
}

func (p *blockstream) GetAccessToken() (string, error) {
	// Fast path: check under read lock first
	p.tokenMu.RLock()
	if p.accessToken != "" && time.Now().Before(p.accessTokenExpirationTime) {
		token := p.accessToken
		p.tokenMu.RUnlock()
		return token, nil
	}
	p.tokenMu.RUnlock()

	// Slow path: fetch a new token under write lock
	p.tokenMu.Lock()
	defer p.tokenMu.Unlock()

	// Re-check after acquiring write lock (another goroutine may have refreshed it)
	if p.accessToken != "" && time.Now().Before(p.accessTokenExpirationTime) {
		return p.accessToken, nil
	}

	if p.clientID == "" || p.clientSecret == "" {
		return "", errors.New("missing API client ID or secret")
	}

	endpoint := p.loginURL + "/realms/blockstream-public/protocol/openid-connect/token"

	data := url.Values{}
	data.Set("client_id", p.clientID)
	data.Set("client_secret", p.clientSecret)
	data.Set("grant_type", "client_credentials")

	req, err := http.NewRequest(
		http.MethodPost,
		endpoint,
		io.NopCloser(strings.NewReader(data.Encode())),
	)
	if err != nil {
		return "", fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("error getting access token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var tokenResponse struct {
		AccessToken      string `json:"access_token"`
		ExpiresIn        int    `json:"expires_in"`
		RefreshExpiresIn int    `json:"refresh_expires_in"`
		TokenType        string `json:"token_type"`
		IDToken          string `json:"id_token"`
		NotBefore        int    `json:"not_before"`
		Scope            string `json:"scope"`
	}

	err = json.NewDecoder(resp.Body).Decode(&tokenResponse)
	if err != nil {
		return "", fmt.Errorf("error decoding response: %w", err)
	}

	p.accessToken = tokenResponse.AccessToken
	p.accessTokenExpirationTime = time.Now().
		Add(time.Duration(tokenResponse.ExpiresIn) * time.Second)

	return p.accessToken, nil
}

// blockstreamHTTPClient is a shared HTTP client for API calls.
var blockstreamHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

func (p *blockstream) callAPI(
	method string,
	endpoint string,
	params map[string]string,
) ([]byte, error) {
	fullURL := p.baseURL + endpoint
	retried := false

	for {
		token, err := p.GetAccessToken()
		if err != nil {
			return nil, fmt.Errorf("failed to get access token: %w", err)
		}

		req, err := http.NewRequest(method, fullURL, nil)
		if err != nil {
			return nil, fmt.Errorf("error creating request: %w", err)
		}

		req.Header.Add("Authorization", "Bearer "+token)

		if params != nil {
			q := req.URL.Query()
			for key, value := range params {
				q.Add(key, value)
			}
			req.URL.RawQuery = q.Encode()
		}

		resp, err := blockstreamHTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("error executing request: %w", err)
		}

		switch resp.StatusCode {
		case http.StatusOK:
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return nil, fmt.Errorf("error reading response body: %w", err)
			}
			return body, nil
		case http.StatusUnauthorized:
			resp.Body.Close()
			if retried {
				return nil, errors.New("unauthorized: token refresh did not resolve the issue")
			}
			retried = true
			// Invalidate cached token so GetAccessToken fetches a fresh one
			p.tokenMu.Lock()
			p.accessToken = ""
			p.tokenMu.Unlock()
		case http.StatusNotFound:
			resp.Body.Close()
			return nil, errors.New("transaction not found")
		case http.StatusTooManyRequests:
			resp.Body.Close()
			return nil, errors.New("rate limit exceeded")
		default:
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("API request failed with status code: %d: %s", resp.StatusCode, string(body))
		}
	}
}

type blockstreamGetTxResponse struct {
	Confirmed   bool    `json:"confirmed"`
	BlockHeight *int    `json:"block_height"`
	BlockHash   *string `json:"block_hash"`
}

func (p *blockstream) getTxStatus(txID string) (blockstreamGetTxResponse, error) {
	body, err := p.callAPI(http.MethodGet, "/tx/"+txID+"/status", nil)
	if err != nil {
		return blockstreamGetTxResponse{}, err
	}

	var response blockstreamGetTxResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return blockstreamGetTxResponse{}, fmt.Errorf("error parsing response: %w", err)
	}

	return response, nil
}

// CheckHealth performs a health check on the Blockstream API
func (p *blockstream) CheckHealth(ctx context.Context) chainkit.HealthStatus {
	start := time.Now()

	url := fmt.Sprintf("%s/blocks/tip/height", p.baseURL)
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

	// Add authentication if configured
	if p.clientID != "" {
		req.Header.Set("X-Client-Id", p.clientID)
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

type BlockstreamFeeEstimates map[string]float64

// getFeeEstimates retrieves fee estimates from Blockstream API
//
// GET /fee-estimates
// Get an object where the key is the confirmation target (in number of blocks) and the value is the estimated feerate (in sat/vB).
//
// The available confirmation targets are 1-25, 144, 504 and 1008 blocks.
//
// For example: { "1": 87.882, "2": 87.882, "3": 87.882, "4": 87.882, "5": 81.129, "6": 68.285, ..., "144": 1.027, "504": 1.027, "1008": 1.027 }
func (p *blockstream) getFeeEstimates(ctx context.Context) (BlockstreamFeeEstimates, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	endpoint := "/fee-estimates"

	body, err := p.callAPI("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch fee estimates from Blockstream: %w", err)
	}

	var feeEstimates BlockstreamFeeEstimates
	err = json.Unmarshal(body, &feeEstimates)
	if err != nil {
		return nil, fmt.Errorf("failed to parse fee estimates response: %w", err)
	}

	return feeEstimates, nil
}

// GetTxFees implements FeeFetcher interface
// Returns fee estimates for different confirmation targets (fast, medium, slow)
func (p *blockstream) GetTxFees(ctx context.Context) ([]types.FeeTier, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	estimates, err := p.getFeeEstimates(ctx)
	if err != nil {
		return nil, err
	}

	// Blockstream provides estimates for blocks 1-25, 144, 504, 1008
	// We'll map to standard fee tiers: fast (1 block), medium (6 blocks), slow (144 blocks)
	var feeTiers []types.FeeTier

	roundFeeRate := func(f float64) uint64 {
		r := uint64(math.Round(f))
		if r < 1 {
			r = 1
		}
		return r
	}

	// Fast: 1 block (highest priority)
	if feeRate, ok := estimates["1"]; ok {
		feeTiers = append(feeTiers, types.FeeTier{
			FeeRate:     roundFeeRate(feeRate),
			TargetBlock: 1,
		})
	}

	// Medium: 6 blocks
	if feeRate, ok := estimates["6"]; ok {
		feeTiers = append(feeTiers, types.FeeTier{
			FeeRate:     roundFeeRate(feeRate),
			TargetBlock: 6,
		})
	}

	// Slow: 144 blocks (~1 day)
	if feeRate, ok := estimates["144"]; ok {
		feeTiers = append(feeTiers, types.FeeTier{
			FeeRate:     roundFeeRate(feeRate),
			TargetBlock: 144,
		})
	}

	if len(feeTiers) == 0 {
		return nil, fmt.Errorf("no fee estimates available from Blockstream")
	}

	return feeTiers, nil
}

// GetTxFee implements FeeFetcher interface
// Returns a specific fee tier by index
func (p *blockstream) GetTxFee(ctx context.Context, feeTier int) (types.FeeTier, error) {
	fees, err := p.GetTxFees(ctx)
	if err != nil {
		return types.FeeTier{}, err
	}

	if feeTier < 0 || feeTier >= len(fees) {
		return types.FeeTier{}, fmt.Errorf("invalid fee tier: %d (available: 0-%d)", feeTier, len(fees)-1)
	}

	return fees[feeTier], nil
}

// GetCapabilities returns the list of capabilities this provider supports
func (p *blockstream) GetCapabilities() []chainkit.ProviderCapability {
	return []chainkit.ProviderCapability{
		chainkit.CapabilityAddressValidation,
		chainkit.CapabilityBalanceFetching,
		chainkit.CapabilityFeeFetching,
		chainkit.CapabilityUTXOFetching,
	}
}
