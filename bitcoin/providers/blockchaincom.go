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
	"github.com/exapsy/chainkit/bitcoin/types"
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
	network    types.BitcoinNetwork
	httpClient *http.Client
}

// NewBlockchainCom creates a BlockchainComProvider for the given network.
// Returns nil for non-mainnet networks — blockchain.info only supports mainnet.
func NewBlockchainCom(network types.BitcoinNetwork) BlockchainComProvider {
	if network != types.BitcoinNetworkMainnet {
		return nil
	}
	return &blockchainCom{
		network:    network,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *blockchainCom) Name() string {
	return "BlockchainCom"
}

// GetBalance fetches the confirmed balance for the given address.
// blockchain.info's Q API returns a plain-text integer (satoshis).
func (p *blockchainCom) GetBalance(ctx context.Context, address string) (chainkit.Balance, error) {
	endpoint := blockchainComBaseURL + "/q/addressbalance/" + address

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return chainkit.Balance{}, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return chainkit.Balance{}, fmt.Errorf("failed to fetch balance from BlockchainCom: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return chainkit.Balance{}, fmt.Errorf("BlockchainCom API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return chainkit.Balance{}, fmt.Errorf("failed to read response body: %w", err)
	}

	sats, err := strconv.ParseUint(strings.TrimSpace(string(body)), 10, 64)
	if err != nil {
		return chainkit.Balance{}, fmt.Errorf("failed to parse balance: %w", err)
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
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to broadcast transaction via BlockchainCom: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("BlockchainCom API returned status %d: %s", resp.StatusCode, string(respBody))
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
func (p *blockchainCom) GetCapabilities() []chainkit.ProviderCapability {
	return []chainkit.ProviderCapability{
		chainkit.CapabilityBalanceFetching,
		chainkit.CapabilityTxBroadcast,
	}
}
