package bitcoin

import (
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
)

// ValidatePublicAddress checks whether the given address is valid for the specified network.
func ValidatePublicAddress(address string, network *chaincfg.Params) (isValid bool) {
	addr, err := btcutil.DecodeAddress(address, network)
	if err != nil {
		return false
	}

	return addr.IsForNet(network)
}
