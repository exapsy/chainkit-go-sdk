package payment

import (
	"errors"
	"fmt"
	"math/big"
	"net/url"
)

type Coin string

const (
	BTC  Coin = "bitcoin"
	ETH  Coin = "ethereum"
	USDT Coin = "tether"
)

type BuildPaymentLinkOptions struct {
	WalletAddress string
	Coin          Coin
	Amount        *big.Int
	Label         string
	Message       string
}

// BuildPaymentLink builds a BIP21 / coin-URI payment link.
func BuildPaymentLink(options BuildPaymentLinkOptions) (string, error) {
	if options.Amount == nil {
		return "", errors.New("amount cannot be nil")
	}

	if options.WalletAddress == "" {
		return "", errors.New("wallet public address cannot be empty")
	}

	if options.Label == "" {
		return "", errors.New("label cannot be empty")
	}

	if options.Message == "" {
		return "", errors.New("message cannot be empty")
	}

	switch options.Coin {
	case BTC:
		// Convert satoshis to BTC for the URI (divide by 100,000,000)
		btcAmount := new(big.Float).Quo(new(big.Float).SetInt(options.Amount), big.NewFloat(1e8))

		return fmt.Sprintf(
			"bitcoin:%s?amount=%s&label=%s&message=%s",
			options.WalletAddress,
			btcAmount.Text('f', 8), // Format as decimal with 8 decimal places
			url.QueryEscape(options.Label),
			url.QueryEscape(options.Message),
		), nil
	case ETH:
		// Convert wei to ETH for the URI (divide by 10^18)
		ethAmount := new(big.Float).Quo(new(big.Float).SetInt(options.Amount), big.NewFloat(1e18))

		return fmt.Sprintf(
			"ethereum:%s?amount=%s&label=%s&message=%s",
			options.WalletAddress,
			ethAmount.Text('f', 18),
			url.QueryEscape(options.Label),
			url.QueryEscape(options.Message),
		), nil
	case USDT:
		// Convert smallest unit to USDT for the URI (divide by 10^6)
		usdtAmount := new(big.Float).Quo(new(big.Float).SetInt(options.Amount), big.NewFloat(1e6))

		return fmt.Sprintf(
			"tether:%s?amount=%s&label=%s&message=%s",
			options.WalletAddress,
			usdtAmount.Text('f', 6),
			url.QueryEscape(options.Label),
			url.QueryEscape(options.Message),
		), nil
	default:
		return "", fmt.Errorf("unsupported coin: %s", options.Coin)
	}
}

// ConvertSatoshisToBitcoin converts a given amount of satoshis to Bitcoin.
//
// Each satoshi is worth 0.00000001 Bitcoin (there are 100,000,000 satoshis in 1 Bitcoin).
func ConvertSatoshisToBitcoin(sat *big.Int) (*big.Float, error) {
	if sat == nil {
		return nil, errors.New("amount cannot be nil")
	}

	const satBitcoinRate = 1e8

	amountFloat := new(big.Float).SetInt(sat)
	amountFloat.Quo(amountFloat, big.NewFloat(satBitcoinRate))

	result := new(big.Float).Mul(amountFloat, big.NewFloat(satBitcoinRate))

	return result, nil
}
