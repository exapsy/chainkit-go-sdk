package bitcoin

import (
	"github.com/btcsuite/btcd/btcutil"
	"github.com/exapsy/chainkit/bitcoin/types"
)

// ValidatePublicAddress checks whether the given address is valid for the
// specified network. Returns (false, nil) for malformed addresses, and
// (false, err) only when the network itself is invalid.
func ValidatePublicAddress(address string, network types.BitcoinNetwork) (bool, error) {
	params, err := network.ChaincfgNetwork()
	if err != nil {
		return false, err
	}

	addr, err := btcutil.DecodeAddress(address, params)
	if err != nil {
		return false, nil
	}

	return addr.IsForNet(params), nil
}
