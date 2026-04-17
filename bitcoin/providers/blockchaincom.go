package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/exapsy/chainkit"
)

const (
	blockchainComBaseURL = "https://blockchain.info"
)

type BlockchainComProvider interface {
	chainkit.BlockchainBaseProvider
	chainkit.TxBroadcaster
	chainkit.BalanceFetcher
	chainkit.HealthChecker
}

type blockchainCom struct {
	httpClient *http.Client
}

// NewBlockchainCom creates a BlockchainComProvider.
// blockchain.info only supports mainnet; callers are responsible for only
// registering this provider when running on mainnet.
func NewBlockchainCom() BlockchainComProvider {
	return &blockchainCom{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *blockchainCom) Name() string {
	return "BlockchainCom"
}

// GetBalance fetches the confirmed balance for the given address.
// blockchain.info's Q API returns a plain-text integer (satoshis).
// A 404 response means the address has no on-chain history (never seen in any
// transaction), which is treated as a zero balance rather than an error.
func (p *blockchainCom) GetBalance(ctx context.Context, address string) (chainkit.Balance, error) {
	endpoint := blockchainComBaseURL + "/q/addressbalance/" + address

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return chainkit.Balance{}, fmt.Errorf("create request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return chainkit.Balance{}, fmt.Errorf("fetch balance for %s: %w", address, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 404 means the address has never appeared in any transaction; treat as zero balance.
	if resp.StatusCode == http.StatusNotFound {
		return chainkit.Balance{}, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return chainkit.Balance{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return chainkit.Balance{}, fmt.Errorf("read response body: %w", err)
	}

	sats, err := strconv.ParseUint(strings.TrimSpace(string(body)), 10, 64)
	if err != nil {
		return chainkit.Balance{}, fmt.Errorf("parse balance: %w", err)
	}

	return chainkit.Balance{
		Confirmed:   sats,
		Unconfirmed: 0,
		Total:       sats,
	}, nil
}

// PushTx broadcasts a signed transaction via blockchain.info's pushtx endpoint.
// The form field name is "tx" and the value is the hex-encoded transaction.
// blockchain.info does not return the txid in the response; it is computed
// deterministically from the raw transaction bytes via double-SHA256.
func (p *blockchainCom) PushTx(ctx context.Context, rawTx []byte) (string, error) {
	formBody := url.Values{"tx": {hex.EncodeToString(rawTx)}}.Encode()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		blockchainComBaseURL+"/pushtx",
		strings.NewReader(formBody),
	)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("broadcast transaction: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// blockchain.info returns plain-text error messages starting with "Failed"
	// even on HTTP 200 when the transaction is rejected.
	if strings.HasPrefix(strings.TrimSpace(string(respBody)), "Failed") {
		return "", fmt.Errorf("transaction rejected by BlockchainCom: %s", strings.TrimSpace(string(respBody)))
	}

	return computeTxID(rawTx), nil
}

// computeTxID derives a Bitcoin transaction ID from raw transaction bytes.
// The txid is the double-SHA256 of the serialized transaction, with bytes
// reversed to match Bitcoin's big-endian display convention.
func computeTxID(rawTx []byte) string {
	first := sha256.Sum256(rawTx)
	second := sha256.Sum256(first[:])
	result := make([]byte, len(second))
	copy(result, second[:])
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return hex.EncodeToString(result)
}

// CheckHealth performs a health check against blockchain.info.
func (p *blockchainCom) CheckHealth(ctx context.Context) chainkit.HealthStatus {
	start := time.Now()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		blockchainComBaseURL+"/q/getblockcount",
		nil,
	)
	if err != nil {
		return chainkit.HealthStatus{
			Status:      chainkit.HealthLevelDown,
			Error:       err.Error(),
			LastChecked: time.Now(),
		}
	}

	resp, err := p.httpClient.Do(req)
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
func (p *blockchainCom) GetCapabilities() []chainkit.ProviderCapability {
	return []chainkit.ProviderCapability{
		chainkit.CapabilityBalanceFetching,
		chainkit.CapabilityTxBroadcast,
	}
}
