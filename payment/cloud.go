package payment

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ============================================================
// Cloud client — push invoice state transitions to chainkit-cloud
// ============================================================
//
// The merchant's app runs its own watcher (the SDK's payment.Watcher
// or equivalent) and observes invoice state transitions before the
// cloud's own poller does. CloudClient is how those transitions
// reach chainkit-cloud over the existing /v1/ingest endpoint.
//
// Wire format mirrors chainkit-cloud-srv's services/ingest/types.go.
// Schema is "chainkit.ingest.v1" — the same envelope cloudagent uses
// for req/score telemetry, with a new event kind "invoice".
//
// Deliberate design choices:
//   - Single-event POSTs, no batching. Invoice transitions are rare
//     vs req-event telemetry; latency matters (paid → confirmed in
//     ~seconds, not the cloudagent's polling interval). The caller
//     decides retry policy — we surface an error and let them
//     re-call.
//   - No streaming / async queue. CloudClient is a stateless thin
//     wrapper around http.Client; safe for concurrent use.
//   - Same Bearer-token auth as the rest of /v1/ingest. The merchant
//     reuses their existing project API key.

// ErrCloudHTTP is returned for non-2xx responses. The HTTP status is
// available via errors.As → *ErrCloudHTTP for callers that want to
// branch on it (e.g. retry on 5xx, fail-fast on 4xx).
type ErrCloudHTTP struct {
	StatusCode int
	Body       string
}

func (e *ErrCloudHTTP) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("payment cloud: HTTP %d: %s", e.StatusCode, e.Body)
	}
	return fmt.Sprintf("payment cloud: HTTP %d", e.StatusCode)
}

// IsRetryable reports whether the caller should retry. 5xx + 429 are
// retryable; 4xx (other than 429) and parse errors are not.
func (e *ErrCloudHTTP) IsRetryable() bool {
	return e.StatusCode >= 500 || e.StatusCode == http.StatusTooManyRequests
}

// CloudClient pushes invoice state transitions to chainkit-cloud.
// Construct with NewCloudClient; safe for concurrent use across
// goroutines.
type CloudClient struct {
	endpoint string
	apiKey   string
	agent    string
	http     *http.Client
}

// CloudClientOption mutates a CloudClient at construction. Used for
// optional knobs (custom http.Client, agent name) without bloating
// the constructor signature.
type CloudClientOption func(*CloudClient)

// WithHTTPClient overrides the default http.Client (timeout 10s).
// Use for tests with httptest, or for callers that want their own
// retry/proxy behaviour wrapped around the transport.
func WithHTTPClient(c *http.Client) CloudClientOption {
	return func(cc *CloudClient) { cc.http = c }
}

// WithAgentName overrides the agent string stamped on every batch.
// Defaults to "chainkit-go-sdk/payment". Helps cloud-side debugging
// when a merchant has multiple integration paths.
func WithAgentName(name string) CloudClientOption {
	return func(cc *CloudClient) {
		if name != "" {
			cc.agent = name
		}
	}
}

// NewCloudClient constructs a client. endpoint may be empty; we
// default to "https://api.chainkit.dev" (the production hostname).
// apiKey is the merchant's project API key, surfaced from the
// chainkit-cloud dashboard at project create time.
func NewCloudClient(apiKey, endpoint string, opts ...CloudClientOption) *CloudClient {
	if endpoint == "" {
		endpoint = "https://api.chainkit.dev"
	}
	cc := &CloudClient{
		endpoint: endpoint,
		apiKey:   apiKey,
		agent:    "chainkit-go-sdk/payment",
		http:     &http.Client{Timeout: 10 * time.Second},
	}
	for _, opt := range opts {
		opt(cc)
	}
	return cc
}

// CloudInvoiceObservation is the merchant-side view of a state
// change to push to the cloud. Mirrors the cloud's
// services/ingest.InvoiceEvent wire shape but lives here so the SDK
// has no source dependency on the cloud module.
//
// Status MUST be one of "pending" | "partial" | "paid" | "confirmed".
// Terminal states (expired / cancelled / refunded) are the cloud's
// responsibility and the SDK has no business pushing them.
type CloudInvoiceObservation struct {
	PublicID      string
	Status        string
	Txid          string
	Confirmations int
	ReceivedSats  int64
	ObservedAt    time.Time
}

// SubmitInvoiceTransition pushes one observation to chainkit-cloud's
// /v1/ingest endpoint. Returns nil on 202 Accepted (the contract),
// *ErrCloudHTTP for non-2xx, or a transport error otherwise.
//
// Idempotency: the ingest endpoint dedups by (project_id, event_id)
// within a 5-minute window. We mint a fresh event_id per call so
// retries from the caller flow as distinct events — combined with
// the cloud's terminal-state guard (post-confirmation pushes are
// quiet no-ops), this is safe to retry.
func (c *CloudClient) SubmitInvoiceTransition(
	ctx context.Context, obs CloudInvoiceObservation,
) error {
	if obs.PublicID == "" {
		return errors.New("payment cloud: PublicID is required")
	}
	if obs.Status == "" {
		return errors.New("payment cloud: Status is required")
	}
	if obs.ObservedAt.IsZero() {
		obs.ObservedAt = time.Now().UTC()
	}

	eventID, err := newEventID()
	if err != nil {
		return fmt.Errorf("payment cloud: event id: %w", err)
	}
	now := time.Now().UTC()

	batch := cloudWireBatch{
		Schema:  "chainkit.ingest.v1",
		Agent:   c.agent,
		BatchID: "p-" + eventID, // "p-" prefix so cloud-side log lines distinguish from cloudagent's
		SentAt:  now,
		Events: []cloudWireEnvelope{{
			ID:   eventID,
			T:    obs.ObservedAt,
			Kind: "invoice",
			Invoice: &cloudWireInvoice{
				PublicID:      obs.PublicID,
				Status:        obs.Status,
				Txid:          obs.Txid,
				Confirmations: obs.Confirmations,
				ReceivedSats:  obs.ReceivedSats,
				ObservedAt:    obs.ObservedAt,
			},
		}},
	}

	body, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("payment cloud: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/ingest", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("payment cloud: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("payment cloud: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusAccepted {
		// 202 is the success contract; drain + return.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	// Capture a short body excerpt for diagnostics. Cloud-side
	// validation errors come back as small JSON blobs.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	return &ErrCloudHTTP{StatusCode: resp.StatusCode, Body: string(raw)}
}

// newEventID returns a 16-hex-char (8-byte) random identifier. The
// cloud ingest treats event_ids as opaque dedup keys; any unique
// string works. Short hex avoids dragging in a ULID dep.
func newEventID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// ============================================================
// Wire types — kept private; only the public API uses
// CloudInvoiceObservation
// ============================================================

type cloudWireBatch struct {
	Schema  string              `json:"schema"`
	Agent   string              `json:"agent"`
	BatchID string              `json:"batch_id"`
	SentAt  time.Time           `json:"sent_at"`
	Events  []cloudWireEnvelope `json:"events"`
}

type cloudWireEnvelope struct {
	ID      string            `json:"id"`
	T       time.Time         `json:"t"`
	Kind    string            `json:"kind"`
	Invoice *cloudWireInvoice `json:"invoice,omitempty"`
}

type cloudWireInvoice struct {
	PublicID      string    `json:"public_id"`
	Status        string    `json:"status"`
	Txid          string    `json:"txid,omitempty"`
	Confirmations int       `json:"confirmations,omitempty"`
	ReceivedSats  int64     `json:"received_sats,omitempty"`
	ObservedAt    time.Time `json:"observed_at"`
}
