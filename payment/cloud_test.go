package payment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// captureIngest records every POST + lets the test set the status
// code returned. Stand-in for chainkit-cloud-srv's /v1/ingest.
type captureIngest struct {
	mu       sync.Mutex
	calls    [][]byte
	authHdrs []string
	status   int32 // atomic
}

func newCaptureIngest(status int) (*captureIngest, *httptest.Server) {
	c := &captureIngest{}
	atomic.StoreInt32(&c.status, int32(status))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.calls = append(c.calls, body)
		c.authHdrs = append(c.authHdrs, r.Header.Get("Authorization"))
		c.mu.Unlock()
		w.WriteHeader(int(atomic.LoadInt32(&c.status)))
		// On 4xx/5xx, return a small JSON blob so the client's
		// ErrCloudHTTP captures something useful.
		if atomic.LoadInt32(&c.status) >= 400 {
			_, _ = w.Write([]byte(`{"error":"forced_test_failure"}`))
		}
	}))
	return c, ts
}

func (c *captureIngest) setStatus(s int) { atomic.StoreInt32(&c.status, int32(s)) }

func (c *captureIngest) calledN() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *captureIngest) lastBody() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		return nil
	}
	return c.calls[len(c.calls)-1]
}

func TestCloudClient_SubmitInvoiceTransition_Success(t *testing.T) {
	cap, srv := newCaptureIngest(http.StatusAccepted)
	defer srv.Close()

	c := NewCloudClient("ck_test_secret", srv.URL)
	err := c.SubmitInvoiceTransition(context.Background(), CloudInvoiceObservation{
		PublicID:      "abc123",
		Status:        "paid",
		Txid:          "tx-deadbeef",
		Confirmations: 0,
		ReceivedSats:  50000,
		ObservedAt:    time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("expected nil on 202, got %v", err)
	}
	if cap.calledN() != 1 {
		t.Fatalf("expected 1 POST, got %d", cap.calledN())
	}

	// Bearer token landed verbatim.
	if got := cap.authHdrs[0]; got != "Bearer ck_test_secret" {
		t.Errorf("Authorization header: got %q", got)
	}

	// Body parses as a v1 ingest batch with a single invoice event.
	var parsed struct {
		Schema  string `json:"schema"`
		Agent   string `json:"agent"`
		BatchID string `json:"batch_id"`
		Events  []struct {
			ID      string `json:"id"`
			Kind    string `json:"kind"`
			Invoice struct {
				PublicID      string `json:"public_id"`
				Status        string `json:"status"`
				Txid          string `json:"txid"`
				Confirmations int    `json:"confirmations"`
				ReceivedSats  int64  `json:"received_sats"`
			} `json:"invoice"`
		} `json:"events"`
	}
	if err := json.Unmarshal(cap.lastBody(), &parsed); err != nil {
		t.Fatalf("body did not parse: %v", err)
	}
	if parsed.Schema != "chainkit.ingest.v1" {
		t.Errorf("schema = %q, want chainkit.ingest.v1", parsed.Schema)
	}
	if !strings.HasPrefix(parsed.BatchID, "p-") {
		t.Errorf("batch_id missing 'p-' prefix: %q", parsed.BatchID)
	}
	if len(parsed.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(parsed.Events))
	}
	ev := parsed.Events[0]
	if ev.Kind != "invoice" {
		t.Errorf("event kind = %q, want invoice", ev.Kind)
	}
	if ev.ID == "" {
		t.Error("event id was empty")
	}
	if ev.Invoice.PublicID != "abc123" || ev.Invoice.Status != "paid" ||
		ev.Invoice.Txid != "tx-deadbeef" || ev.Invoice.ReceivedSats != 50000 {
		t.Errorf("invoice payload mismatch: %+v", ev.Invoice)
	}
}

func TestCloudClient_SubmitInvoiceTransition_Validation(t *testing.T) {
	cap, srv := newCaptureIngest(http.StatusAccepted)
	defer srv.Close()
	c := NewCloudClient("k", srv.URL)

	cases := []struct {
		name string
		obs  CloudInvoiceObservation
		want string
	}{
		{"missing public_id", CloudInvoiceObservation{Status: "paid"}, "PublicID is required"},
		{"missing status", CloudInvoiceObservation{PublicID: "abc"}, "Status is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := c.SubmitInvoiceTransition(context.Background(), tc.obs)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
	// Validation failures must NOT have made a request.
	if cap.calledN() != 0 {
		t.Errorf("expected 0 POSTs from validation failures, got %d", cap.calledN())
	}
}

func TestCloudClient_SubmitInvoiceTransition_5xxRetryable(t *testing.T) {
	cap, srv := newCaptureIngest(http.StatusInternalServerError)
	defer srv.Close()
	c := NewCloudClient("k", srv.URL)

	err := c.SubmitInvoiceTransition(context.Background(), CloudInvoiceObservation{
		PublicID: "abc", Status: "paid", ReceivedSats: 1,
	})
	if err == nil {
		t.Fatal("expected error on 500")
	}
	var httpErr *ErrCloudHTTP
	if !errors.As(err, &httpErr) {
		t.Fatalf("error is not *ErrCloudHTTP: %v", err)
	}
	if httpErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", httpErr.StatusCode)
	}
	if !httpErr.IsRetryable() {
		t.Error("5xx must be retryable")
	}
	if !strings.Contains(httpErr.Body, "forced_test_failure") {
		t.Errorf("error body missing payload: %q", httpErr.Body)
	}

	// 429 is also retryable.
	cap.setStatus(http.StatusTooManyRequests)
	err = c.SubmitInvoiceTransition(context.Background(), CloudInvoiceObservation{
		PublicID: "abc", Status: "paid", ReceivedSats: 1,
	})
	var httpErr2 *ErrCloudHTTP
	if !errors.As(err, &httpErr2) {
		t.Fatalf("429: error is not *ErrCloudHTTP: %v", err)
	}
	if !httpErr2.IsRetryable() {
		t.Error("429 must be retryable")
	}
}

func TestCloudClient_SubmitInvoiceTransition_4xxFailFast(t *testing.T) {
	_, srv := newCaptureIngest(http.StatusUnauthorized)
	defer srv.Close()
	c := NewCloudClient("k", srv.URL)

	err := c.SubmitInvoiceTransition(context.Background(), CloudInvoiceObservation{
		PublicID: "abc", Status: "paid",
	})
	var httpErr *ErrCloudHTTP
	if !errors.As(err, &httpErr) {
		t.Fatalf("error is not *ErrCloudHTTP: %v", err)
	}
	if httpErr.IsRetryable() {
		t.Error("401 must NOT be retryable")
	}
}

func TestCloudClient_AgentNameOverride(t *testing.T) {
	cap, srv := newCaptureIngest(http.StatusAccepted)
	defer srv.Close()

	c := NewCloudClient("k", srv.URL, WithAgentName("merchant-app/1.4.2"))
	_ = c.SubmitInvoiceTransition(context.Background(), CloudInvoiceObservation{
		PublicID: "abc", Status: "paid", ReceivedSats: 1,
	})

	var parsed struct {
		Agent string `json:"agent"`
	}
	_ = json.Unmarshal(cap.lastBody(), &parsed)
	if parsed.Agent != "merchant-app/1.4.2" {
		t.Errorf("agent = %q, want merchant-app/1.4.2", parsed.Agent)
	}
}

func TestCloudClient_DefaultEndpointAndAgent(t *testing.T) {
	// Bare constructor — no httptest server, just verify the
	// defaults landed without making a real request. The actual
	// HTTP call is exercised in the integration-style tests above
	// with custom endpoints.
	c := NewCloudClient("k", "")
	if c.endpoint != "https://api.chainkit.dev" {
		t.Errorf("default endpoint = %q, want https://api.chainkit.dev", c.endpoint)
	}
	if c.agent != "chainkit-go-sdk/payment" {
		t.Errorf("default agent = %q", c.agent)
	}
}

func TestCloudClient_ObservedAtDefaultsToNow(t *testing.T) {
	cap, srv := newCaptureIngest(http.StatusAccepted)
	defer srv.Close()
	c := NewCloudClient("k", srv.URL)

	before := time.Now().UTC().Add(-1 * time.Second)
	_ = c.SubmitInvoiceTransition(context.Background(), CloudInvoiceObservation{
		PublicID: "abc", Status: "paid", ReceivedSats: 1,
		// ObservedAt deliberately zero — client must default it.
	})
	after := time.Now().UTC().Add(1 * time.Second)

	var parsed struct {
		Events []struct {
			Invoice struct {
				ObservedAt time.Time `json:"observed_at"`
			} `json:"invoice"`
		} `json:"events"`
	}
	if err := json.Unmarshal(cap.lastBody(), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := parsed.Events[0].Invoice.ObservedAt
	if got.Before(before) || got.After(after) {
		t.Errorf("observed_at not defaulted to ~now: %v (window %v..%v)", got, before, after)
	}
}
