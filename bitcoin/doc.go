// Package bitcoin contains Bitcoin-specific utilities for the chainkit library.
//
// Sub-packages:
//   - bitcoin/types — core Bitcoin data types (UTXO, Tx, SignedTx, FeeTier, etc.)
//   - bitcoin/providers — Bitcoin provider implementations (Metal, Mempool, BlockCypher, etc.)
//
// Standalone utilities in this package:
//   - [DeriveHDIndices] — BIP32 HD wallet index derivation from an arbitrary string key
//   - [ValidatePublicAddress] — validate a Bitcoin address for a given network
//   - [GenerateKeys] — generate a new secp256k1 key pair
package bitcoin
