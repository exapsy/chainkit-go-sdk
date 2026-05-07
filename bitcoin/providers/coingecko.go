package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/exapsy/chainkit"
	"github.com/exapsy/chainkit/bitcoin/types"
)

const (
	coinGeckoBaseURL = "https://api.coingecko.com/api/v3"
)

type CoinGeckoService interface {
	chainkit.BlockchainBaseProvider
	chainkit.RateFetcher
	chainkit.APIKeyValidator
}

type coingecko struct {
	coinGeckoBaseURL string
	apiKey           string
	httpClient       *http.Client

	// Auth state updated by ValidateAPIKey()
	authMu    sync.RWMutex
	authValid *bool
	authErr   error
}

type CoingeckoOptions struct {
	CoinGeckoBaseURL string
	APIKey           string
}

func NewCoingecko(opts CoingeckoOptions) CoinGeckoService {
	if opts.CoinGeckoBaseURL == "" {
		opts.CoinGeckoBaseURL = coinGeckoBaseURL
	}

	return &coingecko{
		coinGeckoBaseURL: opts.CoinGeckoBaseURL,
		apiKey:           opts.APIKey,
		httpClient:       &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *coingecko) Name() string {
	return "Coingecko"
}

func (s *coingecko) GetExchangeRates(
	ctx context.Context,
	coin types.CoinTicker,
) ([]types.CoinRate, error) {
	// provider name set by manager)

	url := s.coinGeckoBaseURL + "/simple/price?ids=" + coin.CoingeckoString() +
		"&vs_currencies=usd,eur,gbp,jpy,aud,cad,chf,cny,krw,brl,sgd,sek,nzd,hkd,nok,pln,zar,rub,try"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if s.apiKey != "" {
		req.Header.Set("x-cg-pro-api-key", s.apiKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		// Detect authentication/authorization failures and wrap with ErrAuthFailure.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("HTTP %d: %s: %w", resp.StatusCode, string(body), chainkit.ErrAuthFailure)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// map[coin][currency]float64
	type Response map[string]map[string]float64

	var currencyMap Response

	err = json.NewDecoder(resp.Body).Decode(&currencyMap)
	if err != nil {
		return nil, err
	}

	coinRates := make([]types.CoinRate, 0, len(currencyMap))

	for coin, currency := range currencyMap {
		for currencyName, price := range currency {
			rate := big.NewFloat(price)

			var c types.CoinTicker

			switch coin {
			case "bitcoin":
				c = types.CoinTickerBTC
			default:
				return nil, &types.CoinNotSupportedError{
					Coin: coin,
				}
			}

			coinRate := types.CoinRate{
				Coin:      c,
				Currency:  types.Currency(currencyName),
				Rate:      rate,
				Timestamp: time.Now(),
			}

			coinRates = append(coinRates, coinRate)
		}
	}

	return coinRates, nil
}

func (s *coingecko) GetExchangeRate(
	ctx context.Context,
	coin types.CoinTicker,
	currency types.Currency,
) (*types.CoinRate, error) {
	// provider name set by manager)

	url := s.coinGeckoBaseURL + "/simple/price?ids=" + string(
		coin.CoingeckoString(),
	) + "&vs_currencies=" + currency.String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if s.apiKey != "" {
		req.Header.Set("x-cg-pro-api-key", s.apiKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		// Detect authentication/authorization failures and wrap with ErrAuthFailure.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("HTTP %d: %s: %w", resp.StatusCode, string(body), chainkit.ErrAuthFailure)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// CoinGecko response: {"bitcoin": {"usd": 67432.89}}
	priceMap := make(map[string]map[string]float64)

	err = json.NewDecoder(resp.Body).Decode(&priceMap)
	if err != nil {
		return nil, err
	}

	coinPrices, ok := priceMap[coin.CoingeckoString()]
	if !ok {
		return nil, &types.CoinNotSupportedError{Coin: coin.CoingeckoString()}
	}

	price, ok := coinPrices[strings.ToLower(currency.String())]
	if !ok {
		return nil, &types.CurrencyNotSupportedError{
			Currency: currency.String(),
		}
	}

	rate := big.NewFloat(price)

	return &types.CoinRate{
		Coin:      coin,
		Currency:  currency,
		Rate:      rate,
		Timestamp: time.Now(),
	}, nil
}

// CheckHealth performs a health check on the CoinGecko API
func (s *coingecko) CheckHealth(ctx context.Context) chainkit.HealthStatus {
	start := time.Now()

	url := s.coinGeckoBaseURL + "/ping"
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
	if s.apiKey != "" {
		req.Header.Set("x-cg-pro-api-key", s.apiKey)
	}

	resp, err := s.httpClient.Do(req)
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

	// Read cached auth state
	s.authMu.RLock()
	authValid := s.authValid
	authErr := s.authErr
	s.authMu.RUnlock()

	var authErrStr string
	if authErr != nil {
		authErrStr = authErr.Error()
	}

	// Coingecko has a public fallback, so if auth is invalid we degrade but don't go down
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
		AuthError:      authErrStr,
		IsDegraded:     isDegraded,
	}
}

// GetCapabilities returns the list of capabilities this provider supports
func (s *coingecko) GetCapabilities() []chainkit.ProviderCapability {
	return []chainkit.ProviderCapability{
		chainkit.CapabilityAPIKeyValidation,
		chainkit.CapabilityRateFetching,
	}
}

// ValidateAPIKey validates the configured API key with CoinGecko's API.
// Returns nil if the key is valid or if no key is configured (public API).
// Updates internal auth state that will be reflected in CheckHealth().
func (s *coingecko) ValidateAPIKey(ctx context.Context) error {
	// If no API key configured, we're using the public API - always valid
	if s.apiKey == "" {
		s.authMu.Lock()
		s.authValid = nil // nil means no auth required
		s.authErr = nil
		s.authMu.Unlock()
		return nil
	}

	url := s.coinGeckoBaseURL + "/ping"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		s.authMu.Lock()
		authFalse := false
		s.authValid = &authFalse
		s.authErr = fmt.Errorf("create request: %w", err)
		s.authMu.Unlock()
		return s.authErr
	}
	req.Header.Set("x-cg-pro-api-key", s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.authMu.Lock()
		authFalse := false
		s.authValid = &authFalse
		s.authErr = fmt.Errorf("validate API key: %w", err)
		s.authMu.Unlock()
		return s.authErr
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		s.authMu.Lock()
		authFalse := false
		s.authValid = &authFalse
		s.authErr = fmt.Errorf("invalid API key (HTTP %d): %w", resp.StatusCode, chainkit.ErrAuthFailure)
		s.authMu.Unlock()
		return s.authErr
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		s.authMu.Lock()
		authFalse := false
		s.authValid = &authFalse
		s.authErr = fmt.Errorf("rate limit exceeded")
		s.authMu.Unlock()
		return s.authErr
	}

	// Success - mark auth as valid
	s.authMu.Lock()
	authTrue := true
	s.authValid = &authTrue
	s.authErr = nil
	s.authMu.Unlock()

	return nil
}
