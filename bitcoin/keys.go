package bitcoin

import (
	"github.com/btcsuite/btcd/btcec/v2"
)

// GenerateKeys generates a new secp256k1 private/public key pair.
func GenerateKeys() (*btcec.PrivateKey, *btcec.PublicKey, error) {
	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		return nil, nil, err
	}

	return privKey, privKey.PubKey(), nil
}
