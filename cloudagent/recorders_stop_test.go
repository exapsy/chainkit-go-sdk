package cloudagent

import (
	"context"
	"net/http"
	"net/http/httptest"
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

	rec.Stop()

	if got.Load() == 0 {
		t.Fatal("Stop() returned without flushing the pending event to /v1/ingest")
	}
}

// TestMetricsRecorder_StopIdempotent — double Stop must not panic or hang.
func TestMetricsRecorder_StopIdempotent(t *testing.T) {
	rec := NewMetricsRecorder(Options{Endpoint: "", APIKey: "x"}) // noop transport
	rec.Stop()
	rec.Stop()
}
