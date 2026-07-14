package cloudagent

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/exapsy/chainkit"
)

// TestMetricsRecorder_StopFlushesPending is the quickstart-program contract:
// a recorder that captured one event and is Stop()ped immediately must still
// deliver that event, even though neither the batch size nor the flush
// interval was reached. Without Stop, a short-lived program exits with its
// telemetry still in the buffer and the dashboard never sees the event.
func TestMetricsRecorder_StopFlushesPending(t *testing.T) {
	var got atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/ingest" {
			got.Add(1)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	rec := NewMetricsRecorder(Options{
		Endpoint: srv.URL,
		APIKey:   "ck_test_stop_flush",
		// A flush interval far beyond the test's lifetime, so the ONLY
		// way the event ships is the Stop drain under test.
		FlushInterval: time.Hour,
	})
	rec.RecordBlockchainRequestRich(context.Background(), chainkit.RequestEvent{
		Provider:  "test",
		Operation: "get_balance",
		Success:   true,
		Duration:  5 * time.Millisecond,
	})

	if err := rec.Stop(); err != nil {
		t.Fatalf("Stop() = %v, want nil (delivery succeeded)", err)
	}

	if got.Load() == 0 {
		t.Fatal("Stop() returned without flushing the pending event to /v1/ingest")
	}
}

// TestMetricsRecorder_StopIdempotent — double Stop must not panic or hang.
func TestMetricsRecorder_StopIdempotent(t *testing.T) {
	rec := NewMetricsRecorder(Options{Endpoint: "", APIKey: "x"}) // noop transport
	_ = rec.Stop()
	_ = rec.Stop()
}

// TestMetricsRecorder_AuthRejectionIsTerminalAndLoud is the invalid_key
// contract: a 401 from /v1/ingest must (a) surface from Stop() with a
// message pointing at the API key, (b) log exactly one Error record,
// (c) fire OnError, and (d) short-circuit every later flush — auth does
// not heal, so no more HTTP attempts after the first rejection.
func TestMetricsRecorder_AuthRejectionIsTerminalAndLoud(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelError}))
	var onErrCalls atomic.Int64

	rec := NewMetricsRecorder(Options{
		Endpoint:      srv.URL,
		APIKey:        "ck_live_bogus",
		FlushInterval: time.Hour, // only the Stop drain may flush
		Logger:        logger,
		OnError:       func(error) { onErrCalls.Add(1) },
	})
	ev := chainkit.RequestEvent{Provider: "test", Operation: "get_balance", Success: true}
	rec.RecordBlockchainRequestRich(context.Background(), ev)
	// Second event recorded after the terminal state will exist by the
	// time Stop drains — both must die without a second HTTP attempt.
	rec.RecordBlockchainRequestRich(context.Background(), ev)

	err := rec.Stop()
	if err == nil {
		t.Fatal("Stop() = nil, want the API-key rejection error")
	}
	if !strings.Contains(err.Error(), "API key") {
		t.Fatalf("Stop() error %q does not mention the API key", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hit %d times, want exactly 1 (terminal state must short-circuit retries)", hits.Load())
	}
	if onErrCalls.Load() == 0 {
		t.Fatal("OnError was never invoked for the auth rejection")
	}
	if !strings.Contains(logBuf.String(), "telemetry disabled") {
		t.Fatalf("expected one loud Error log record, got: %q", logBuf.String())
	}
}

// TestMetricsRecorder_StopSurfacesDrainFailure — a transient failure during
// the shutdown drain (here a 500) must come back from Stop() as an
// undelivered-events error instead of being swallowed.
func TestMetricsRecorder_StopSurfacesDrainFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	var onErrCalls atomic.Int64
	rec := NewMetricsRecorder(Options{
		Endpoint:      srv.URL,
		APIKey:        "ck_test_drain",
		FlushInterval: time.Hour,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnError:       func(error) { onErrCalls.Add(1) },
	})
	rec.RecordBlockchainRequestRich(context.Background(), chainkit.RequestEvent{
		Provider: "test", Operation: "get_balance", Success: true,
	})

	err := rec.Stop()
	if err == nil {
		t.Fatal("Stop() = nil, want an undelivered-events error")
	}
	if !strings.Contains(err.Error(), "undelivered") {
		t.Fatalf("Stop() error %q does not mention undelivered events", err)
	}
	if onErrCalls.Load() == 0 {
		t.Fatal("OnError was never invoked for the drain failure")
	}
}
