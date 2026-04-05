package providers

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/exapsy/chainkit"
	"github.com/exapsy/chainkit/bitcoin/types"
)

const (
	bitrefMainnetBaseURL = "https://api.bitref.com"
)

type BitrefcomProvider interface {
	chainkit.BlockchainBaseProvider
	chainkit.TxBroadcaster
	chainkit.FeeRecommender
	chainkit.BalanceFetcher
}

type Bitrefcom struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

type BitrefcomOptions struct {
	APIKey string
	// BaseURL overrides the default API base URL. Resolved by the caller from
	// config/providers/bitrefcom.yaml for the active network. If empty, defaults
	// to the mainnet API URL.
	BaseURL string
}

func NewBitrefcom(opts BitrefcomOptions) BitrefcomProvider {
	if opts.BaseURL == "" {
		opts.BaseURL = bitrefMainnetBaseURL
	}

	return &Bitrefcom{
		apiKey:     opts.APIKey,
		baseURL:    opts.BaseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (b *Bitrefcom) Name() string {
	return "Bitrefcom"
}

func (b *Bitrefcom) callAPI(
	ctx context.Context,
	method string,
	endpoint string,
	body io.Reader,
) ([]byte, error) {
	if b.baseURL == "" {
		return nil, errors.New("base URL not set")
	}

	if b.apiKey == "" {
		return nil, errors.New("API key not set")
	}

	if endpoint == "" {
		return nil, errors.New("endpoint not set")
	}

	req, err := http.NewRequestWithContext(ctx, method, b.baseURL+endpoint, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Api-Key", b.apiKey)

	// Set Content-Type for requests with body
	if body != nil && (method == http.MethodPost || method == http.MethodPut) {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return responseBody, nil
}

func (b *Bitrefcom) GetTxFee(ctx context.Context, priority types.FeePriority) (types.FeeTier, error) {
	endpoint := fmt.Sprintf("/v1/fees/estimate/%d", priority.TargetBlock())

	body, err := b.callAPI(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return types.FeeTier{}, err
	}

	/*
			{
		  	"feerate": 4.629,
		  	"blocks": 6
		  }
	*/
	type response struct {
		FeeRate float64 `json:"feerate"`
		Blocks  int     `json:"blocks"`
	}

	var resp response

	err = json.Unmarshal(body, &resp)
	if err != nil {
		return types.FeeTier{}, err
	}

	feeRate := uint64(math.Round(resp.FeeRate))
	if feeRate < 1 {
		feeRate = 1
	}
	return types.FeeTier{
		FeeRate:     feeRate,
		TargetBlock: resp.Blocks,
	}, nil
}

func (b *Bitrefcom) GetTxFees(ctx context.Context) ([]types.FeeTier, error) {
	endpoint := "/v1/fees/estimates"

	body, err := b.callAPI(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	/*
			{
		  	"1": 6.976,
		  	"2": 6.976,
		  	"3": 6.212,
		  	[...]
		  	"142": 3.144,
		  	"143": 3.144,
		  	"144": 3.144
		  }
	*/
	type response map[string]float64

	var resp response

	err = json.Unmarshal(body, &resp)
	if err != nil {
		return nil, err
	}

	feeTiers := make([]types.FeeTier, 0, len(resp))

	for block, feeRate := range resp {
		blockNum := 0
		if _, err := fmt.Sscanf(block, "%d", &blockNum); err != nil {
			continue
		}
		rounded := uint64(math.Round(feeRate))
		if rounded < 1 {
			rounded = 1
		}
		feeTiers = append(feeTiers, types.FeeTier{
			FeeRate:     rounded,
			TargetBlock: blockNum,
		})
	}

	return feeTiers, nil
}

// PushTx broadcasts a raw transaction to the Bitcoin network.
func (b *Bitrefcom) PushTx(ctx context.Context, rawTx []byte) (txID string, err error) {
	endpoint := "/v1/tx/broadcast"

	// Prepare request body
	reqBody := struct {
		RawTx string `json:"rawTx"`
	}{
		RawTx: hex.EncodeToString(rawTx), // Hex encode the raw transaction
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request body: %w", err)
	}

	// Make the API call
	responseBody, err := b.callAPI(
		ctx,
		http.MethodPost,
		endpoint,
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return "", fmt.Errorf("broadcast transaction: %w", err)
	}

	// Parse the response
	var response struct {
		TxID string `json:"txId"`
	}

	if err := json.Unmarshal(responseBody, &response); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	return response.TxID, nil
}

// balanceResponse represents the API response structure for balance queries
type balanceResponse struct {
	ConfirmedBalance   uint64 `json:"confirmed_balance"`
	UnconfirmedBalance uint64 `json:"unconfirmed_balance"`
}

// fetchBalanceData is a helper method that fetches balance data from the API
func (b *Bitrefcom) fetchBalanceData(ctx context.Context, address string) (*balanceResponse, error) {
	endpoint := fmt.Sprintf("/v1/address/%s/balance", address)
	body, err := b.callAPI(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch address details: %w", err)
	}

	var resp balanceResponse
	err = json.Unmarshal(body, &resp)
	if err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &resp, nil
}

// GetBalance fetches balance data once and returns confirmed, unconfirmed, and total.
func (b *Bitrefcom) GetBalance(ctx context.Context, address string) (chainkit.Balance, error) {
	resp, err := b.fetchBalanceData(ctx, address)
	if err != nil {
		return chainkit.Balance{}, err
	}

	return chainkit.Balance{
		Confirmed:   resp.ConfirmedBalance,
		Unconfirmed: resp.UnconfirmedBalance,
		Total:       resp.ConfirmedBalance + resp.UnconfirmedBalance,
	}, nil
}

// CheckHealth performs a health check on the Bitref.com API
func (b *Bitrefcom) CheckHealth(ctx context.Context) chainkit.HealthStatus {
	start := time.Now()

	// Bitref.com only supports mainnet - if baseURL is empty, provider is not configured
	if b.baseURL == "" {
		return chainkit.HealthStatus{
			Status:         chainkit.HealthLevelDown,
			ResponseTimeMs: 0,
			ResponseTimeUs: 0,
			Error:          "Bitref.com only supports Bitcoin mainnet",
			LastChecked:    time.Now(),
		}
	}

	// Use the fees endpoint for health check (no dedicated health endpoint exists)
	url := fmt.Sprintf("%s/v1/fees/estimates", b.baseURL)

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return chainkit.HealthStatus{
			Status:         chainkit.HealthLevelDown,
			ResponseTimeMs: 0,
			ResponseTimeUs: 0,
			Error:          err.Error(),
			LastChecked:    time.Now(),
		}
	}

	// Add API key if configured
	if b.apiKey != "" {
		req.Header.Set("X-API-Key", b.apiKey)
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
func (b *Bitrefcom) GetCapabilities() []chainkit.ProviderCapability {
	return []chainkit.ProviderCapability{
		chainkit.CapabilityBalanceFetching,
		chainkit.CapabilityFeeRecommending,
		chainkit.CapabilityFeeEstimation,
		chainkit.CapabilityTxBroadcast,
	}
}
