package bitcoin

import (
	"github.com/exapsy/chainkit"
	"github.com/exapsy/chainkit/bitcoin/types"
)

// CalculateBalanceFromUTXOs computes confirmed, unconfirmed, and total balances
// from a slice of UTXOs without making any network calls.
func CalculateBalanceFromUTXOs(utxos []types.UTXO) chainkit.Balance {
	var confirmed, unconfirmed uint64
	for _, u := range utxos {
		if u.Confirmed {
			confirmed += uint64(u.Amount)
		} else {
			unconfirmed += uint64(u.Amount)
		}
	}
	return chainkit.Balance{
		Confirmed:   confirmed,
		Unconfirmed: unconfirmed,
		Total:       confirmed + unconfirmed,
	}
}
