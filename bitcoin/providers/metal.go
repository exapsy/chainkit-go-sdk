package providers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/exapsy/chainkit"
	"github.com/exapsy/chainkit/bitcoin/types"
)

type MetalProvider interface {
	chainkit.BlockchainBaseProvider
	chainkit.AddressGenerator
	chainkit.FeeEstimator
	chainkit.TxAssembler
	chainkit.TxSigner
	chainkit.TxSizer
	chainkit.AddressValidator
}

// metal implements only local operations that don't require network connectivity:
// - AddressGenerator (for key derivation)
// - FeeEstimator (for basic fee calculation)
// - TxAssembler (for transaction creation)
// - TxSigner (for transaction signing)
// - TxSizer (for size calculation)
//
// It intentionally does NOT implement:
// - FeeFetcher (requires network API)
// - UTXOFetcher (requires network API)
// - TxBroadcaster (requires network API)
// - TxStatusFetcher (requires network API)
//
// Use with MixedProviders to combine with network-capable providers.
type metal struct {
	network types.BitcoinNetwork
}

type MetalProviderOptions struct {
	Network types.BitcoinNetwork
}

func NewMetal(opts MetalProviderOptions) MetalProvider {
	return &metal{
		network: opts.Network,
	}
}

func (p *metal) Name() string {
	return "Metal"
}

func (p *metal) DeriveAddress(ctx context.Context, xpub string, index uint32, childIndex uint32) (chainkit.DerivedAddress, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	extendedKey, err := hdkeychain.NewKeyFromString(xpub)
	if err != nil {
		return chainkit.DerivedAddress{}, fmt.Errorf("key parsing failed: %w", err)
	}

	net, err := p.network.ChaincfgNetwork()
	if err != nil {
		return chainkit.DerivedAddress{}, err
	}

	extendedKey.SetNet(net)

	childKey, err := extendedKey.Derive(index)
	if err != nil {
		return chainkit.DerivedAddress{}, err
	}

	address, err := childKey.Derive(childIndex)
	if err != nil {
		return chainkit.DerivedAddress{}, err
	}

	addr, err := address.Address(net)
	if err != nil {
		return chainkit.DerivedAddress{}, err
	}

	privKey, err := address.ECPrivKey()
	switch {
	case err == nil:
		// continue
	case errors.Is(err, hdkeychain.ErrNotPrivExtKey):
		return chainkit.DerivedAddress{
			PublicKey: addr.EncodeAddress(),
			Mode:      chainkit.PublicKeyOnly,
		}, nil
	default:
		return chainkit.DerivedAddress{}, err
	}

	wif, err := btcutil.NewWIF(privKey, net, true)
	if err != nil {
		return chainkit.DerivedAddress{}, err
	}

	return chainkit.DerivedAddress{
		PublicKey:  addr.EncodeAddress(),
		PrivateKey: wif.String(),
		Mode:       chainkit.PublicAndPrivateKey,
	}, nil
}

func (p *metal) CalculateFee(ctx context.Context, signedTxSize uint64, feePerByte uint64) (uint64, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	return signedTxSize * feePerByte, nil
}

func (p *metal) SignTransaction(
	ctx context.Context,
	tx *types.Tx,
	utxos []types.UTXO,
	privWIF string,
) (*types.SignedTx, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	if tx == nil {
		return nil, errors.New("nil pointer: transaction cannot be nil")
	}

	// Verify we have UTXO info for each input
	if len(utxos) != len(tx.Inputs) {
		return nil, fmt.Errorf(
			"mismatched inputs and UTXOs: expected %d inputs, got %d UTXOs",
			len(tx.Inputs),
			len(utxos),
		)
	}

	w, err := btcutil.DecodeWIF(privWIF)
	if err != nil {
		return nil, fmt.Errorf("malformed private key: %w", err)
	}

	wireTx := wire.NewMsgTx(tx.Version)

	netParams, err := p.network.ChaincfgNetwork()
	if err != nil {
		return nil, err
	}

	if !w.IsForNet(netParams) {
		return nil, errors.New("private key is not for the specified network")
	}

	// Add inputs to wireTx
	for _, input := range tx.Inputs {
		hash, err := chainhash.NewHashFromStr(input.PreviousTxID)
		if err != nil {
			return nil, fmt.Errorf("invalid previous transaction ID: %w", err)
		}

		prevOut := wire.OutPoint{
			Hash:  *hash,
			Index: input.OutputIndex,
		}

		txIn := wire.NewTxIn(&prevOut, nil, nil)
		wireTx.AddTxIn(txIn)
	}

	outputPkScripts := make([][]byte, len(tx.Outputs))

	// Add outputs
	for i, output := range tx.Outputs {
		addr, err := btcutil.DecodeAddress(output.Address, netParams)
		if err != nil {
			return nil, fmt.Errorf("invalid address: %w", err)
		}

		pkScript, err := txscript.PayToAddrScript(addr)
		if err != nil {
			return nil, err
		}

		outputPkScripts[i] = pkScript

		txOut := wire.NewTxOut(output.Value.Int64(), pkScript)

		wireTx.AddTxOut(txOut)
	}

	// Create a PrevOutputFetcher for the updated NewTxSigHashes function
	prevOuts := make(map[wire.OutPoint]*wire.TxOut)

	for i, utxo := range utxos {
		prevOut := wireTx.TxIn[i].PreviousOutPoint
		txOut := &wire.TxOut{
			Value:    utxo.Amount,
			PkScript: utxo.ScriptPubKey,
		}
		prevOuts[prevOut] = txOut
	}

	prevOutputFetcher := txscript.NewMultiPrevOutFetcher(prevOuts)

	// Sign inputs using the UTXO scriptPubKeys
	for i, utxo := range utxos {
		// Detect script type from scriptPubKey
		scriptClass := txscript.GetScriptClass(utxo.ScriptPubKey)

		switch scriptClass {
		case txscript.PubKeyHashTy:
			// Legacy P2PKH - continue with current approach
			sigScript, err := txscript.SignatureScript(
				wireTx,
				i,
				utxo.ScriptPubKey,
				txscript.SigHashAll,
				w.PrivKey,
				true,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to sign input %d: %w", i, err)
			}

			wireTx.TxIn[i].SignatureScript = sigScript
		case txscript.WitnessV0PubKeyHashTy:
			// Native SegWit P2WPKH
			witnessSignature, err := txscript.WitnessSignature(
				wireTx,
				txscript.NewTxSigHashes(wireTx, prevOutputFetcher),
				i,
				utxo.Amount,
				utxo.ScriptPubKey,
				txscript.SigHashAll,
				w.PrivKey,
				true,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"failed to create witness signature for input %d: %w",
					i,
					err,
				)
			}

			wireTx.TxIn[i].Witness = witnessSignature
		case txscript.ScriptHashTy:
			// Could be P2SH-wrapped SegWit
			// More complex - need to detect if it's P2SH-P2WPKH or P2SH-P2WSH
			// This requires extracting the redeem script
			addrHash := btcutil.Hash160(w.SerializePubKey())

			redeemScript, err := txscript.NewScriptBuilder().
				AddData([]byte{0x00, 0x14}).
				AddData(addrHash).
				Script()
			if err != nil {
				return nil, fmt.Errorf("failed to create redeem script for input %d: %w", i, err)
			}

			// Create signature using witness program
			witnessProgram := redeemScript[2:] // Skip the first 2 bytes (0x00, 0x14)

			witnessSignature, err := txscript.WitnessSignature(
				wireTx,
				txscript.NewTxSigHashes(wireTx, prevOutputFetcher),
				i,
				utxo.Amount,
				witnessProgram,
				txscript.SigHashAll,
				w.PrivKey,
				true,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"failed to create witness signature for input %d: %w",
					i,
					err,
				)
			}

			// For P2SH-P2WPKH, signature script is just the redeem script
			wireTx.TxIn[i].SignatureScript = append(
				[]byte{byte(len(redeemScript))},
				redeemScript...)
			wireTx.TxIn[i].Witness = witnessSignature
		default:
			return nil, fmt.Errorf("unsupported script type for input %d: %s", i, scriptClass)
		}
	}

	var buf bytes.Buffer
	if err := wireTx.Serialize(&buf); err != nil {
		return nil, fmt.Errorf("failed to serialize transaction: %w", err)
	}

	signedTx := &types.SignedTx{
		ID:        wireTx.TxHash().String(),
		Version:   tx.Version,
		Inputs:    tx.Inputs,
		Outputs:   tx.Outputs,
		RawSigned: buf.Bytes(),
		Fee:       tx.Fee,
		Status:    tx.Status,
	}

	return signedTx, nil
}

func (p *metal) CalculateTransactionSize(ctx context.Context, tx *types.SignedTx) (uint64, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	return uint64(len(tx.RawSigned)), nil
}

func (p *metal) CreateTransaction(ctx context.Context, utxos []types.UTXO, outputs []types.TxOutput) (*types.Tx, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	if len(utxos) == 0 {
		return nil, errors.New("no inputs provided")
	}

	if len(outputs) == 0 {
		return nil, errors.New("no outputs provided")
	}

	// Create a new transaction
	tx := &types.Tx{
		Version: 2, // Default to version 2
		Inputs:  make([]*types.TxInput, 0, len(utxos)),
		Outputs: make([]*types.TxOutput, 0, len(outputs)),
		Status:  types.TxStatusPending,
	}

	// Add inputs from UTXOs
	for _, utxo := range utxos {
		tx.Inputs = append(tx.Inputs, &types.TxInput{
			PreviousTxID: utxo.TxHash,
			OutputIndex:  utxo.Vout,
		})
	}

	// Add outputs
	for _, output := range outputs {
		tx.Outputs = append(tx.Outputs, &output)
	}

	return tx, nil
}

func (p *metal) ValidateAddress(ctx context.Context, address string) (bool, error) {
	ctx = chainkit.WithProviderName(ctx, p.Name())

	// Decode the address to check its validity
	netParams, err := p.network.ChaincfgNetwork()
	if err != nil {
		return false, err
	}

	_, err = btcutil.DecodeAddress(address, netParams)
	if err != nil {
		return false, nil // Invalid address
	}
	return true, nil // Valid address
}

func getBitcoinNetworkFromExtendedKey(extendedKey *hdkeychain.ExtendedKey) types.BitcoinNetwork {
	// Check each network explicitly
	if extendedKey.IsForNet(&chaincfg.MainNetParams) {
		return types.BitcoinNetworkMainnet
	}
	if extendedKey.IsForNet(&chaincfg.TestNet3Params) {
		return types.BitcoinNetworkTestnet3
	}
	if extendedKey.IsForNet(&chaincfg.RegressionNetParams) {
		return types.BitcoinNetworkRegtest
	}
	if extendedKey.IsForNet(&chaincfg.SimNetParams) {
		return types.BitcoinNetworkSimnet
	}

	return types.BitcoinNetworkUnknown
}

// CheckHealth performs a health check on the Metal provider (local/offline)
func (p *metal) CheckHealth(ctx context.Context) chainkit.HealthStatus {
	start := time.Now()

	// Metal is a local provider (address generation, signing, etc.)
	// It doesn't make external API calls, so health check is instant
	responseDuration := time.Since(start)
	responseTimeMs := responseDuration.Milliseconds()
	responseTimeUs := responseDuration.Microseconds()

	return chainkit.HealthStatus{
		Status:         "healthy",
		ResponseTimeMs: responseTimeMs,
		ResponseTimeUs: responseTimeUs,
		HTTPStatus:     0, // Not applicable for local provider
		Error:          "",
		LastChecked:    time.Now(),
	}
}

// GetCapabilities returns the list of capabilities this provider supports
func (p *metal) GetCapabilities() []chainkit.ProviderCapability {
	return []chainkit.ProviderCapability{
		chainkit.CapabilityAddressGeneration,
		chainkit.CapabilityAddressValidation,
		chainkit.CapabilityFeeEstimation, // CalculateFee
		chainkit.CapabilityTxAssembly,
		chainkit.CapabilityTxSigning,
		chainkit.CapabilityTxSizing, // CalculateTransactionSize
	}
}
