package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/exapsy/chainkit"
	"github.com/exapsy/chainkit/bitcoin/types"
)

const (
	binanceBaseURL = "https://api.binance.com"
)

type BinanceProvider interface {
	chainkit.BlockchainBaseProvider
	chainkit.RateFetcher
	chainkit.HealthChecker
}

type binance struct {
	httpClient *http.Client
}

// NewBinance creates a BinanceProvider using the public Binance ticker API.
// No API key is required.
func NewBinance() BinanceProvider {
	return &binance{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (b *binance) Name() string {
	return "Binance"
}

// currencyToSymbol maps a Currency to the Binance BTC trading pair symbol.
// Only USD and EUR are supported.
func currencyToSymbol(currency types.Currency) (string, bool) {
	switch currency {
	case types.CurrencyUSD:
		return "BTCUSDT", true
	case types.CurrencyEUR:
		return "BTCEUR", true
	default:
		return "", false
	}
}

// fetchPrice fetches the current BTC price for the given Binance symbol.
// The Binance ticker response returns price as a quoted decimal string.
func (b *binance) fetchPrice(ctx context.Context, symbol string) (float64, error) {
	url := binanceBaseURL + "/api/v3/ticker/price?symbol=" + symbol

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var ticker struct {
		Symbol string `json:"symbol"`
		Price  string `json:"price"`
	}

	if err = json.NewDecoder(resp.Body).Decode(&ticker); err != nil {
		return 0, err
	}

	price, err := strconv.ParseFloat(ticker.Price, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse price %q: %w", ticker.Price, err)
	}

	return price, nil
}

// GetExchangeRate returns the current BTC exchange rate for the requested currency.
// Only CoinTickerBTC with USD or EUR is supported.
func (b *binance) GetExchangeRate(
	ctx context.Context,
	coin types.CoinTicker,
	currency types.Currency,
) (*types.CoinRate, error) {
	if coin != types.CoinTickerBTC {
		return nil, &types.CoinNotSupportedError{Coin: coin.BlockchainString()}
	}

	symbol, ok := currencyToSymbol(currency)
	if !ok {
		return nil, &types.CurrencyNotSupportedError{Currency: currency.String()}
	}

	price, err := b.fetchPrice(ctx, symbol)
	if err != nil {
		return nil, err
	}

	return &types.CoinRate{
		Coin:      coin,
		Currency:  currency,
		Rate:      big.NewFloat(price),
		Timestamp: time.Now(),
	}, nil
}

// GetExchangeRates returns BTC rates for all supported currencies (USD and EUR).
func (b *binance) GetExchangeRates(
	ctx context.Context,
	coin types.CoinTicker,
) ([]types.CoinRate, error) {
	if coin != types.CoinTickerBTC {
		return nil, &types.CoinNotSupportedError{Coin: coin.BlockchainString()}
	}

	supported := []struct {
		currency types.Currency
		symbol   string
	}{
		{types.CurrencyUSD, "BTCUSDT"},
		{types.CurrencyEUR, "BTCEUR"},
	}

	rates := make([]types.CoinRate, 0, len(supported))

	for _, s := range supported {
		price, err := b.fetchPrice(ctx, s.symbol)
		if err != nil {
			return nil, err
		}

		rates = append(rates, types.CoinRate{
			Coin:      coin,
			Currency:  s.currency,
			Rate:      big.NewFloat(price),
			Timestamp: time.Now(),
		})
	}

	return rates, nil
}

// CheckHealth performs a health check against the Binance API.
func (b *binance) CheckHealth(ctx context.Context) chainkit.HealthStatus {
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, binanceBaseURL+"/api/v3/ping", nil)
	if err != nil {
		return chainkit.HealthStatus{
			Status:      chainkit.HealthLevelDown,
			Error:       err.Error(),
			LastChecked: time.Now(),
		}
	}

	resp, err := b.httpClient.Do(req)
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
func (b *binance) GetCapabilities() []chainkit.ProviderCapability {
	return []chainkit.ProviderCapability{
		chainkit.CapabilityRateFetching,
	}
}
