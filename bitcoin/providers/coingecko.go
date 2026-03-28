package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
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
}

type coingecko struct {
	coinGeckoBaseURL string
}

type CoingeckoOptions struct {
	CoinGeckoBaseURL string
}

func NewCoingecko(options *CoingeckoOptions) CoinGeckoService {
	if options == nil {
		options = &CoingeckoOptions{
			CoinGeckoBaseURL: coinGeckoBaseURL,
		}
	}

	if options.CoinGeckoBaseURL == "" {
		options.CoinGeckoBaseURL = coinGeckoBaseURL
	}

	return &coingecko{
		coinGeckoBaseURL: options.CoinGeckoBaseURL,
	}
}

func (s *coingecko) Name() string {
	return "Coingecko"
}

func (s *coingecko) GetExchangeRates(
	ctx context.Context,
	coin types.CoinTicker,
) ([]types.CoinRate, error) {
	ctx = chainkit.WithProviderName(ctx, s.Name())

	url := s.coinGeckoBaseURL + "/simple/price?ids=" + coin.CoingeckoString() +
		"&vs_currencies=usd,eur,gbp,jpy,aud,cad,chf,cny,krw,brl,sgd,sek,nzd,hkd,nok,pln,zar,rub,try"

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

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
	ctx = chainkit.WithProviderName(ctx, s.Name())

	url := s.coinGeckoBaseURL + "/simple/price?ids=" + string(
		coin.CoingeckoString(),
	) + "&vs_currencies=" + currency.String()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	currencyMap := make(map[string]float64)

	err = json.NewDecoder(resp.Body).Decode(&currencyMap)
	if err != nil {
		return nil, err
	}

	price, ok := currencyMap[currency.String()]
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

	url := "https://api.coingecko.com/api/v3/ping"
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

// GetCapabilities returns the list of capabilities this provider supports
func (s *coingecko) GetCapabilities() []chainkit.ProviderCapability {
	return []chainkit.ProviderCapability{
		chainkit.CapabilityRateFetching,
	}
}
