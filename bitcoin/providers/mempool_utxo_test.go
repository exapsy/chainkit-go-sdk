package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/exapsy/chainkit/bitcoin/types"
)

// A confirmed UTXO's Confirmations must reflect the real depth
// (tip - blockHeight + 1), not a fixed 1 — otherwise a consumer that
// requires N>1 confirmations can never treat a payment as final.
func TestMempoolGetUTXOs_ConfirmationsFromTip(t *testing.T) {
	const (
		utxoBlockHeight = 100
		tipHeight       = 105 // 6 confirmations deep
		address         = "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/blocks/tip/height":
			_, _ = fmt.Fprintf(w, "%d", tipHeight)
		case "/address/" + address + "/utxo":
			_, _ = fmt.Fprintf(w, `[{"txid":"aa","vout":0,"status":{"confirmed":true,"block_height":%d},"value":50000}]`, utxoBlockHeight)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := NewMempool(MempoolOptions{Network: types.BitcoinNetworkMainnet, BaseURL: srv.URL})

	utxos, err := m.GetUTXOs(context.Background(), address)
	if err != nil {
		t.Fatalf("GetUTXOs: %v", err)
	}
	if len(utxos) != 1 {
		t.Fatalf("got %d utxos, want 1", len(utxos))
	}
	if got, want := utxos[0].Confirmations, int64(tipHeight-utxoBlockHeight+1); got != want {
		t.Errorf("Confirmations = %d, want %d (tip %d - block %d + 1)", got, want, tipHeight, utxoBlockHeight)
	}
}

// When the tip is unreachable, a confirmed UTXO falls back to 1
// confirmation rather than failing the whole call.
func TestMempoolGetUTXOs_TipUnavailableFallsBackToOne(t *testing.T) {
	const address = "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/blocks/tip/height":
			http.Error(w, "boom", http.StatusInternalServerError)
		case "/address/" + address + "/utxo":
			_, _ = fmt.Fprint(w, `[{"txid":"aa","vout":0,"status":{"confirmed":true,"block_height":100},"value":50000}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := NewMempool(MempoolOptions{Network: types.BitcoinNetworkMainnet, BaseURL: srv.URL})

	utxos, err := m.GetUTXOs(context.Background(), address)
	if err != nil {
		t.Fatalf("GetUTXOs: %v", err)
	}
	if len(utxos) != 1 {
		t.Fatalf("got %d utxos, want 1", len(utxos))
	}
	if utxos[0].Confirmations != 1 {
		t.Errorf("Confirmations = %d, want 1 (tip-unavailable fallback)", utxos[0].Confirmations)
	}
}

// An unconfirmed (mempool) UTXO has 0 confirmations regardless of tip.
func TestMempoolGetUTXOs_UnconfirmedIsZero(t *testing.T) {
	const address = "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/blocks/tip/height":
			_, _ = fmt.Fprint(w, "105")
		case "/address/" + address + "/utxo":
			_, _ = fmt.Fprint(w, `[{"txid":"aa","vout":0,"status":{"confirmed":false},"value":50000}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := NewMempool(MempoolOptions{Network: types.BitcoinNetworkMainnet, BaseURL: srv.URL})

	utxos, err := m.GetUTXOs(context.Background(), address)
	if err != nil {
		t.Fatalf("GetUTXOs: %v", err)
	}
	if len(utxos) != 1 {
		t.Fatalf("got %d utxos, want 1", len(utxos))
	}
	if utxos[0].Confirmations != 0 {
		t.Errorf("Confirmations = %d, want 0 (unconfirmed)", utxos[0].Confirmations)
	}
}
