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
	chainkit.HistoricalRateFetcher
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

// GetHistoricalRates returns a time-series of exchange rates over the
// requested window via CoinGecko's `/coins/{id}/market_chart/range` endpoint.
//
// CoinGecko's resolution policy (free public API):
//   - windows ≤ 1 day:  ~5-minute granularity
//   - 1–90 days:        ~hourly granularity
//   - > 90 days:        daily granularity
//
// We don't override the resolution; the API auto-selects based on the
// requested range. Output is normalised into []types.CoinRate ordered
// chronologically (oldest first) — each point carries its own Timestamp,
// so callers can render time-series without a separate timestamp array.
func (s *coingecko) GetHistoricalRates(
	ctx context.Context,
	coin types.CoinTicker,
	currency types.Currency,
	since, until time.Time,
) ([]types.CoinRate, error) {
	if since.IsZero() || until.IsZero() || !since.Before(until) {
		return nil, fmt.Errorf("coingecko: invalid window since=%v until=%v", since, until)
	}

	// CoinGecko expects UNIX seconds in `from`/`to`. The `market_chart/range`
	// variant (vs the `days=` shortcut) lets us pin exact windows without
	// boundary surprises around the day cutover.
	//
	// Two API quirks worth knowing:
	//   1. The coin id segment is CASE-SENSITIVE on this endpoint (the
	//      `/simple/price` endpoint with the `ids=` query param is not),
	//      so we lowercase it. `CoingeckoString()` for BTC returns
	//      "Bitcoin"; the API needs "bitcoin".
	//   2. The endpoint REJECTS the default Go http client User-Agent
	//      with a 401. `/simple/price` accepts it but this one is
	//      stricter. We set an explicit, polite User-Agent identifying
	//      chainkit so the request gets through.
	url := fmt.Sprintf(
		"%s/coins/%s/market_chart/range?vs_currency=%s&from=%d&to=%d",
		s.coinGeckoBaseURL,
		strings.ToLower(coin.CoingeckoString()),
		strings.ToLower(currency.String()),
		since.Unix(),
		until.Unix(),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "chainkit-sdk/1 (+https://github.com/exapsy/chainkit)")
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
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("HTTP %d: %s: %w", resp.StatusCode, string(body), chainkit.ErrAuthFailure)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Response shape: { "prices": [[ts_ms, price], ...], "market_caps": [...], "total_volumes": [...] }
	var payload struct {
		Prices [][]float64 `json:"prices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode coingecko market_chart: %w", err)
	}

	out := make([]types.CoinRate, 0, len(payload.Prices))
	for _, p := range payload.Prices {
		if len(p) < 2 {
			continue
		}
		out = append(out, types.CoinRate{
			Coin:      coin,
			Currency:  currency,
			Rate:      big.NewFloat(p[1]),
			Timestamp: time.UnixMilli(int64(p[0])).UTC(),
			Source:    "Coingecko",
		})
	}

	return out, nil
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
