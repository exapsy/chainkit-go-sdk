package types

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcd/btcutil"
)

type BitcoinNetwork string

const (
	BitcoinNetworkUnknown  BitcoinNetwork = "unknown"
	BitcoinNetworkMainnet  BitcoinNetwork = "mainnet"
	BitcoinNetworkTestnet3 BitcoinNetwork = "testnet3"
	BitcoinNetworkTestnet4 BitcoinNetwork = "testnet4"
	BitcoinNetworkRegtest  BitcoinNetwork = "regtest"
	BitcoinNetworkSimnet   BitcoinNetwork = "simnet"
)

func (n BitcoinNetwork) IsValid() bool {
	switch n {
	case BitcoinNetworkMainnet, BitcoinNetworkTestnet3, BitcoinNetworkTestnet4, BitcoinNetworkRegtest, BitcoinNetworkSimnet:
		return true
	default:
		return false
	}
}

func (b BitcoinNetwork) ChaincfgNetwork() (*chaincfg.Params, error) {
	switch b {
	case BitcoinNetworkMainnet:
		return &chaincfg.MainNetParams, nil
	case BitcoinNetworkTestnet3:
		return &chaincfg.TestNet3Params, nil
	case BitcoinNetworkTestnet4:
		// Testnet4 uses the same address encoding and HD derivation parameters as
		// testnet3 (same tb1/m/n prefixes, same BIP32 version bytes, same coin type).
		// btcd has no dedicated Testnet4Params, so TestNet3Params is the correct substitute.
		return &chaincfg.TestNet3Params, nil
	case BitcoinNetworkRegtest:
		return &chaincfg.RegressionNetParams, nil
	case BitcoinNetworkSimnet:
		return &chaincfg.SimNetParams, nil
	default:
		return nil, fmt.Errorf("unknown Bitcoin network: %s", b)
	}
}

func (n BitcoinNetwork) String() string {
	switch n {
	case BitcoinNetworkMainnet:
		return "mainnet"
	case BitcoinNetworkTestnet3:
		return "testnet3"
	case BitcoinNetworkTestnet4:
		return "testnet4"
	case BitcoinNetworkRegtest:
		return "regtest"
	case BitcoinNetworkSimnet:
		return "simnet"
	default:
		return ""
	}
}

// BlockcypherChain returns the Blockcypher API chain identifier for this network.
// Blockcypher supports mainnet ("main") and testnet3 ("test3") only; testnet4 is
// mapped to "test3" as the closest equivalent. Returns ("", false) for unsupported
// networks (regtest, simnet).
func (n BitcoinNetwork) BlockcypherChain() (string, bool) {
	switch n {
	case BitcoinNetworkMainnet:
		return "main", true
	case BitcoinNetworkTestnet3, BitcoinNetworkTestnet4:
		return "test3", true
	default:
		return "", false
	}
}

type RequestError struct {
	Message string `json:"message"`
	Err     error  `json:"-"`
}

func (e *RequestError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("request error: %s: %v", e.Message, e.Err)
	}

	return "request error: " + e.Message
}

type CoinNotSupportedError struct {
	Coin string
}

func (e *CoinNotSupportedError) Error() string {
	return fmt.Errorf("coin not supported: %s", e.Coin).Error()
}

type UnknownConversionError struct {
	From string
	To   string
	Err  error
}

func (e *UnknownConversionError) Error() string {
	return fmt.Errorf("unknown conversion from %s to %s: %w", e.From, e.To, e.Err).Error()
}

type CurrencyNotSupportedError struct {
	Currency string
}

func (e *CurrencyNotSupportedError) Error() string {
	return fmt.Errorf("currency not supported: %s", e.Currency).Error()
}

type InvalidNetworkError struct {
	Network BitcoinNetwork
}

func (e *InvalidNetworkError) Error() string {
	return fmt.Errorf("invalid Bitcoin network: %s", e.Network).Error()
}

type InvalidNetworkForXPubError struct {
	Network     BitcoinNetwork
	XPub        string
	XPubNetwork BitcoinNetwork
}

func (e *InvalidNetworkForXPubError) Error() string {
	return fmt.Errorf("xpub %s is for network %q, not %q", e.XPub, e.XPubNetwork, e.Network).Error()
}

type FeeTier struct {
	// FeeRate in satoshis per virtual byte
	FeeRate uint64 `json:"fee_rate"`
	// TargetBlock is the block height at which the transaction will be confirmed
	// The higher, the longer it takes to confirm the transaction
	// Starts from the next to confirm block (1)
	TargetBlock int `json:"target_block"`
}

// FeePriority is a named confirmation-speed preference. Each provider adapter
// maps a FeePriority to its own internal representation (e.g. a block target
// or a named tier).
type FeePriority int

const (
	// FeePriorityFastest targets the next block (~10 min).
	FeePriorityFastest FeePriority = iota
	// FeePriorityFast targets confirmation within ~30 min (≈3 blocks).
	FeePriorityFast
	// FeePriorityMedium targets confirmation within ~1 hour (≈6 blocks).
	FeePriorityMedium
	// FeePrioritySlow targets confirmation within ~1 day (≈144 blocks).
	FeePrioritySlow
	// FeePriorityMinimum is the cheapest possible fee (≈1008 blocks, ~1 week).
	FeePriorityMinimum
)

// TargetBlock returns the canonical block-target associated with this priority.
func (p FeePriority) TargetBlock() int {
	switch p {
	case FeePriorityFastest:
		return 1
	case FeePriorityFast:
		return 3
	case FeePriorityMedium:
		return 6
	case FeePrioritySlow:
		return 144
	case FeePriorityMinimum:
		return 1008
	default:
		return 6
	}
}

// SelectClosest picks the FeeTier from tiers whose TargetBlock is closest to
// p.TargetBlock(). Returns the zero value if tiers is empty.
func (p FeePriority) SelectClosest(tiers []FeeTier) FeeTier {
	if len(tiers) == 0 {
		return FeeTier{}
	}

	target := p.TargetBlock()
	best := tiers[0]
	bestDiff := abs(best.TargetBlock - target)

	for _, t := range tiers[1:] {
		if d := abs(t.TargetBlock - target); d < bestDiff {
			bestDiff = d
			best = t
		}
	}

	return best
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

type TxState struct {
	Status string `json:"status"`
}

var (
	TxStateFailed    = TxState{Status: "failed"}
	TxStatePending   = TxState{Status: "pending"}
	TxStateConfirmed = TxState{Status: "confirmed"}
	TxStateUnknown   = TxState{Status: "unknown"}
)

type TxEvent struct {
	TxId          string
	Height        uint64
	Confirmations uint64
	Error         error // Error is set if the transaction failed to be confirmed
}

type SignedTx struct {
	ID        string      `json:"id,omitempty"` // ID will be set after serialization
	Version   int32       `json:"version"`
	Inputs    []*TxInput  `json:"inputs"`
	Outputs   []*TxOutput `json:"outputs"`
	RawSigned []byte      `json:"raw_signed"` // Raw signed transaction bytes
	Fee       uint64      `json:"fee"`        // Transaction fee in satoshis
	Status    TxState     `json:"status"`     // Transaction status
}

type TxOutput struct {
	Address string `json:"address"`
	Value   int64  `json:"value"` // Amount in satoshis
}

// UTXO represents an unspent transaction output that can be used as an input in a new transaction.
type UTXO struct {
	// Core UTXO identification
	TxHash string `json:"tx_hash"` // Transaction ID containing this output (hex string)
	Vout   uint32 `json:"vout"`    // Output index in the transaction

	// Value information
	Amount int64 `json:"amount"` // Amount in satoshis

	// Script data needed for spending
	ScriptPubKey []byte `json:"script_pub_key"` // The locking script that must be satisfied to spend

	// Optional metadata (useful but not required for tx creation)
	Address       string `json:"address"`       // Bitcoin address associated with this UTXO
	Confirmed     bool   `json:"confirmed"`     // Whether the funding transaction is confirmed
	Confirmations int64  `json:"confirmations"` // Number of confirmations (0 if unconfirmed)
	Spendable     bool   `json:"spendable"`     // Whether this UTXO is spendable by the wallet
	BlockHeight   int32  `json:"block_height"`  // Height of the block containing the transaction
}

// OutPoint returns a wire.OutPoint for this UTXO.
func (u *UTXO) OutPoint() (*wire.OutPoint, error) {
	hash, err := chainhash.NewHashFromStr(u.TxHash)
	if err != nil {
		return nil, err
	}

	return wire.NewOutPoint(hash, u.Vout), nil
}

// TxInput creates a wire.TxIn from this UTXO (with empty SignatureScript).
func (u *UTXO) TxInput() (*wire.TxIn, error) {
	outpoint, err := u.OutPoint()
	if err != nil {
		return nil, err
	}

	return wire.NewTxIn(outpoint, nil, nil), nil
}

// ValueInBTC returns the UTXO amount in BTC rather than satoshis.
func (u *UTXO) ValueInBTC() float64 {
	return float64(u.Amount) / 100000000
}

type UTXOSet struct {
	UTXOs []*UTXO `json:"utxos"`
	Total int64   `json:"total"`
}

func (us *UTXOSet) Select(targetAmount int64) ([]UTXO, int64, error) {
	// Select minimum amount of UTXOs to meet target amount and fee rate
	selectedUTXOs := make([]UTXO, 0)
	changeAmount := int64(0)

	for _, utxo := range us.UTXOs {
		selectedUTXOs = append(selectedUTXOs, *utxo)
		changeAmount += utxo.Amount

		if changeAmount >= targetAmount {
			break
		}
	}

	if changeAmount < targetAmount {
		return nil, 0, errors.New("insufficient funds")
	}

	return selectedUTXOs, changeAmount - targetAmount, nil
}

type TxInput struct {
	PreviousTxID string `json:"previous_tx_id"`
	OutputIndex  uint32 `json:"output_index"`
}

type Tx struct {
	Version int32
	Inputs  []*TxInput
	Outputs []*TxOutput
	Fee     uint64
	Status  TxState
	// Params must be set to the network's chaincfg.Params so that Serialize and Hash
	// can produce valid scriptPubKeys for the outputs. If nil, Serialize returns an error.
	Params *chaincfg.Params
}

func (tx *Tx) MarshalJSON() ([]byte, error) {
	type JsonTx struct {
		ID      string      `json:"id,omitempty"` // ID will be set after serialization
		Version int32       `json:"version"`
		Inputs  []*TxInput  `json:"inputs"`
		Outputs []*TxOutput `json:"outputs"`
		Fee     uint64      `json:"fee"`
		Status  TxState     `json:"status"`
	}

	jsonTx := JsonTx{
		Version: tx.Version,
		Inputs:  tx.Inputs,
		Outputs: tx.Outputs,
		Fee:     tx.Fee,
		Status:  tx.Status,
	}

	hash, err := tx.Hash()
	if err != nil {
		return nil, fmt.Errorf("failed to compute transaction hash: %w", err)
	}

	jsonTx.ID = hash

	return json.Marshal(jsonTx)
}

func (tx *Tx) Serialize() ([]byte, error) {
	if tx.Params == nil {
		return nil, errors.New("Tx.Params must be set to network chaincfg.Params before serializing")
	}

	wireTx := wire.NewMsgTx(tx.Version)

	for _, input := range tx.Inputs {
		prevOut, err := chainhash.NewHashFromStr(input.PreviousTxID)
		if err != nil {
			return nil, fmt.Errorf("invalid previous tx id: %w", err)
		}

		wireTx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(prevOut, input.OutputIndex), nil, nil))
	}

	for _, output := range tx.Outputs {
		addr, err := btcutil.DecodeAddress(output.Address, tx.Params)
		if err != nil {
			return nil, fmt.Errorf("invalid output address %q: %w", output.Address, err)
		}

		pkScript, err := txscript.PayToAddrScript(addr)
		if err != nil {
			return nil, fmt.Errorf("failed to build scriptPubKey for %q: %w", output.Address, err)
		}

		wireTx.AddTxOut(wire.NewTxOut(output.Value, pkScript))
	}

	var buf bytes.Buffer
	if err := wireTx.Serialize(&buf); err != nil {
		return nil, fmt.Errorf("failed to serialize transaction: %w", err)
	}

	return buf.Bytes(), nil
}

func (tx *Tx) Hash() (string, error) {
	// Serialize the transaction and compute its hash
	serialized, err := tx.Serialize()
	if err != nil {
		return "", fmt.Errorf("failed to serialize transaction: %w", err)
	}

	hash := chainhash.HashH(serialized)

	return hash.String(), nil
}

type CoinRate struct {
	// Rate in microsatoshis per BTC
	Rate *big.Float `json:"rate"`
	// Timestamp of the rate
	Timestamp time.Time `json:"timestamp"`
	// Network for which the rate is applicable
	Network BitcoinNetwork `json:"network"`
	// Source of the rate (e.g., "blockcypher", "bitrefcom")
	Source string `json:"source"`
	// Currency code (e.g. "USD")
	// Currency is the fiat currency for which the rate is applicable
	Currency Currency `json:"currency"`
	// Coin ticker (e.g., "BTC")
	Coin CoinTicker `json:"coin"`
}

func (r CoinRate) MarshalJSON() ([]byte, error) {
	type Alias CoinRate

	rate, _ := r.Rate.Float64()

	return json.Marshal(&struct {
		Rate float64 `json:"rate"`
		*Alias
	}{
		Rate:  rate,
		Alias: (*Alias)(&r),
	})
}

type CoinTicker struct {
	blockchain string
	coingecko  string
	mempool    string
}

var (
	CoinTickerBTC         = CoinTicker{"BTC", "Bitcoin", "BTC"}
	CoinTickerUnspecified = CoinTicker{"", "", ""}
)

func (t CoinTicker) String() string {
	if t.blockchain == "" && t.coingecko == "" && t.mempool == "" {
		return "Unknown"
	}

	if t.blockchain != "" {
		return t.blockchain
	}

	if t.coingecko != "" {
		return t.coingecko
	}

	if t.mempool != "" {
		return t.mempool
	}

	return "Unknown"
}

func (t CoinTicker) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"%s"`, t.String())), nil
}

// UnmarshalJSON parses a JSON string into a CoinTicker using [CoinTickerFromString].
// Accepted values: "BTC", "Bitcoin", "COIN_TICKER_BTC", "" or "COIN_TICKER_UNSPECIFIED".
func (t *CoinTicker) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := CoinTickerFromString(s)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

func (t CoinTicker) BlockchainString() string {
	if t.blockchain == "" {
		return t.coingecko
	}

	return t.blockchain
}

func (t CoinTicker) CoingeckoString() string {
	if t.coingecko == "" {
		return t.blockchain
	}

	return t.coingecko
}

func (t CoinTicker) MempoolString() string {
	if t.mempool == "" {
		return t.blockchain
	}

	return t.mempool
}

func CoinTickerFromString(s string) (CoinTicker, error) {
	switch s {
	case "COIN_TICKER_BTC", "BTC", "Bitcoin":
		return CoinTickerBTC, nil
	case "COIN_TICKER_UNSPECIFIED", "":
		return CoinTickerUnspecified, nil
	default:
		return CoinTickerUnspecified, fmt.Errorf("unknown coin ticker: %s", s)
	}
}

type Currency string

func (c Currency) String() string {
	return string(c)
}

const (
	CurrencyUSD Currency = "USD"
	CurrencyEUR Currency = "EUR"
	CurrencyGBP Currency = "GBP"
	CurrencyJPY Currency = "JPY"
	CurrencyCAD Currency = "CAD"
)
