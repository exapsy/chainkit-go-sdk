package chainkit

import (
	"context"

	"github.com/exapsy/chainkit/bitcoin/types"
)

// NewRateFetcherChain returns a RateFetcher that tries each provided fetcher in order
// until one succeeds. If a single fetcher is provided, it is returned as-is.
func NewRateFetcherChain(fetchers ...RateFetcher) RateFetcher {
	if len(fetchers) == 1 {
		return fetchers[0]
	}

	return &rateFetcherChain{fetchers: fetchers}
}

type rateFetcherChain struct {
	fetchers []RateFetcher
}

func (c *rateFetcherChain) GetExchangeRate(
	ctx context.Context,
	coin types.CoinTicker,
	currency types.Currency,
) (*types.CoinRate, error) {
	var lastErr error

	for _, f := range c.fetchers {
		rate, err := f.GetExchangeRate(ctx, coin, currency)
		if err == nil {
			return rate, nil
		}

		lastErr = err
	}

	return nil, lastErr
}

func (c *rateFetcherChain) GetExchangeRates(
	ctx context.Context,
	coin types.CoinTicker,
) ([]types.CoinRate, error) {
	var lastErr error

	for _, f := range c.fetchers {
		rates, err := f.GetExchangeRates(ctx, coin)
		if err == nil {
			return rates, nil
		}

		lastErr = err
	}

	return nil, lastErr
}
