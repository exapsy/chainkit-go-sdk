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
	// Label is an optional human-readable label for the recipient (BIP21).
	Label string
	// Message is an optional human-readable description for the transaction (BIP21).
	Message string
}

// BuildPaymentLink builds a BIP21 / coin-URI payment link.
// Label and Message are optional; omit them by leaving the fields empty.
func BuildPaymentLink(options BuildPaymentLinkOptions) (string, error) {
	if options.Amount == nil {
		return "", errors.New("amount cannot be nil")
	}

	if options.WalletAddress == "" {
		return "", errors.New("wallet public address cannot be empty")
	}

	switch options.Coin {
	case BTC:
		btcAmount := new(big.Float).Quo(new(big.Float).SetInt(options.Amount), big.NewFloat(1e8))
		uri := fmt.Sprintf("bitcoin:%s?amount=%s", options.WalletAddress, btcAmount.Text('f', 8))
		uri = appendOptionalParam(uri, "label", options.Label)
		uri = appendOptionalParam(uri, "message", options.Message)
		return uri, nil
	case ETH:
		ethAmount := new(big.Float).Quo(new(big.Float).SetInt(options.Amount), big.NewFloat(1e18))
		uri := fmt.Sprintf("ethereum:%s?amount=%s", options.WalletAddress, ethAmount.Text('f', 18))
		uri = appendOptionalParam(uri, "label", options.Label)
		uri = appendOptionalParam(uri, "message", options.Message)
		return uri, nil
	case USDT:
		usdtAmount := new(big.Float).Quo(new(big.Float).SetInt(options.Amount), big.NewFloat(1e6))
		uri := fmt.Sprintf("tether:%s?amount=%s", options.WalletAddress, usdtAmount.Text('f', 6))
		uri = appendOptionalParam(uri, "label", options.Label)
		uri = appendOptionalParam(uri, "message", options.Message)
		return uri, nil
	default:
		return "", fmt.Errorf("unsupported coin: %s", options.Coin)
	}
}

// appendOptionalParam appends &key=value to uri only when value is non-empty.
func appendOptionalParam(uri, key, value string) string {
	if value == "" {
		return uri
	}
	return uri + "&" + key + "=" + url.QueryEscape(value)
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

	return amountFloat, nil
}
