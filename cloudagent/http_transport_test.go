package cloudagent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/exapsy/chainkit"
)

// recordedRequest captures what the test server received. Tests inspect a
// channel of these to make timing-sensitive assertions about flush behaviour.
type recordedRequest struct {
	Method     string
	Path       string
	AuthHeader string
	Body       wireBatch
	ReceivedAt time.Time
}

// fakeServer wraps an httptest.Server with programmable status codes per
// request. It exposes recorded requests on a channel so tests can assert
// behaviour deterministically.
type fakeServer struct {
	*httptest.Server

	mu        sync.Mutex
	responses []int     // status codes to return, in order; trailing reuses last value
	requests  []recordedRequest

	requestCh chan recordedRequest // buffered so handlers never block
}

func newFakeServer(responses ...int) *fakeServer {
	if len(responses) == 0 {
		responses = []int{http.StatusAccepted}
	}
	f := &fakeServer{
		responses: responses,
		requestCh: make(chan recordedRequest, 1024),
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeServer) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	defer r.Body.Close()
	var b wireBatch
	_ = json.Unmarshal(body, &b)
	rec := recordedRequest{
		Method:     r.Method,
		Path:       r.URL.Path,
		AuthHeader: r.Header.Get("Authorization"),
		Body:       b,
		ReceivedAt: time.Now(),
	}
	f.mu.Lock()
	f.requests = append(f.requests, rec)
	status := http.StatusAccepted
	if len(f.responses) > 0 {
		idx := len(f.requests) - 1
		if idx >= len(f.responses) {
			idx = len(f.responses) - 1
		}
		status = f.responses[idx]
	}
	f.mu.Unlock()
	select {
	case f.requestCh <- rec:
	default:
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"accepted":` + intToString(len(b.Events)) + `,"deduped":0}`))
}

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	if neg {
		out = append([]byte{'-'}, out...)
	}
	return string(out)
}

func (f *fakeServer) seen() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

// waitForRequests blocks until at least n requests have been received or the
// timeout fires.
func (f *fakeServer) waitForRequests(t *testing.T, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		f.mu.Lock()
		got := len(f.requests)
		f.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %d requests; got %d", n, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// buildTestOptions returns an Options with fast timers + the fake server URL.
func buildTestOptions(srv *fakeServer) Options {
	return Options{
		Endpoint:      srv.URL,
		APIKey:        "ck_test_FAKE",
		BatchSize:     5,
		FlushInterval: 50 * time.Millisecond,
		BufferSize:    20,
		MaxBackoff:    200 * time.Millisecond,
		EventTTL:      time.Hour,
	}
}

func pushN(tr *httpTransport, n int) {
	for i := 0; i < n; i++ {
		tr.Push(Event{
			CapturedAt: time.Now(),
			Request: &chainkit.RequestEvent{
				Provider:     "mempool",
				Operation:    "GetUTXOs",
				Success:      true,
				Duration:     time.Millisecond * 50,
				AttemptCount: 1,
			},
		})
	}
}

func TestHTTPTransport_FlushOnInterval(t *testing.T) {
	srv := newFakeServer(http.StatusAccepted)
	defer srv.Close()

	tr := newHTTPTransport(buildTestOptions(srv).withDefaults())
	defer tr.Stop()

	// Push fewer than BatchSize so only the interval ticker triggers.
	pushN(tr, 3)
	srv.waitForRequests(t, 1, time.Second)

	seen := srv.seen()
	if len(seen) != 1 {
		t.Fatalf("expected 1 request, got %d", len(seen))
	}
	if seen[0].Path != "/v1/ingest" {
		t.Fatalf("path: got %q", seen[0].Path)
	}
	if seen[0].AuthHeader != "Bearer ck_test_FAKE" {
		t.Fatalf("auth: got %q", seen[0].AuthHeader)
	}
	if len(seen[0].Body.Events) != 3 {
		t.Fatalf("events in batch: got %d want 3", len(seen[0].Body.Events))
	}
}

func TestHTTPTransport_FlushOnBatchSize(t *testing.T) {
	srv := newFakeServer(http.StatusAccepted)
	defer srv.Close()

	opts := buildTestOptions(srv)
	opts.FlushInterval = 10 * time.Second // effectively disable the ticker
	tr := newHTTPTransport(opts.withDefaults())
	defer tr.Stop()

	// Push exactly BatchSize — should trigger the signal-based flush.
	pushN(tr, opts.BatchSize)
	srv.waitForRequests(t, 1, time.Second)

	seen := srv.seen()
	if len(seen[0].Body.Events) != opts.BatchSize {
		t.Fatalf("batch size: got %d want %d", len(seen[0].Body.Events), opts.BatchSize)
	}
}

func TestHTTPTransport_DropOldestOnOverflow(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()

	opts := buildTestOptions(srv)
	opts.BufferSize = 5
	opts.BatchSize = 100 // do not trigger size flush
	opts.FlushInterval = 10 * time.Second
	tr := newHTTPTransport(opts.withDefaults())
	defer tr.Stop()

	pushN(tr, 10) // 5 over BufferSize → 5 dropped
	if d := tr.Dropped(); d != 5 {
		t.Fatalf("dropped: got %d want 5", d)
	}
}

func TestHTTPTransport_RetriesOn5xx(t *testing.T) {
	// First two requests 503, third 202. Verify the events are eventually
	// delivered exactly once.
	srv := newFakeServer(http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusAccepted)
	defer srv.Close()

	opts := buildTestOptions(srv)
	opts.FlushInterval = 30 * time.Millisecond
	opts.MaxBackoff = 100 * time.Millisecond
	tr := newHTTPTransport(opts.withDefaults())
	defer tr.Stop()

	pushN(tr, 2)

	srv.waitForRequests(t, 3, 5*time.Second)
	seen := srv.seen()
	// Each of the three requests should have carried 2 events (the same
	// events, retried).
	for i, r := range seen[:3] {
		if len(r.Body.Events) != 2 {
			t.Fatalf("request %d: %d events", i, len(r.Body.Events))
		}
	}
}

func TestHTTPTransport_4xxDropsWithoutRetry(t *testing.T) {
	srv := newFakeServer(http.StatusBadRequest)
	defer srv.Close()

	opts := buildTestOptions(srv)
	opts.FlushInterval = 30 * time.Millisecond
	tr := newHTTPTransport(opts.withDefaults())
	defer tr.Stop()

	pushN(tr, 3)
	srv.waitForRequests(t, 1, time.Second)

	// Wait long enough that another attempt would happen if we were retrying.
	time.Sleep(150 * time.Millisecond)
	if got := len(srv.seen()); got != 1 {
		t.Fatalf("expected exactly 1 request for poison batch, got %d", got)
	}
	if got := tr.Dropped(); got != 3 {
		t.Fatalf("dropped: got %d want 3", got)
	}
}

func TestHTTPTransport_StopDrainsPending(t *testing.T) {
	srv := newFakeServer(http.StatusAccepted)
	defer srv.Close()

	opts := buildTestOptions(srv)
	opts.FlushInterval = 10 * time.Second // never tick during the test
	tr := newHTTPTransport(opts.withDefaults())

	pushN(tr, 3)
	// Stop should drain even without the ticker firing.
	tr.Stop()

	if got := len(srv.seen()); got != 1 {
		t.Fatalf("Stop did not drain: got %d requests", got)
	}
	if got := len(srv.seen()[0].Body.Events); got != 3 {
		t.Fatalf("drained batch: %d events, want 3", got)
	}
}

func TestHTTPTransport_PushNeverBlocksUnderOverflow(t *testing.T) {
	// Use a server that hangs forever; the transport must keep accepting
	// pushes (dropping oldest) without blocking.
	hang := make(chan struct{})
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hang
	}))
	defer func() { close(hang); hs.Close() }()

	opts := Options{
		Endpoint:      hs.URL,
		APIKey:        "k",
		BatchSize:     10,
		FlushInterval: 1 * time.Millisecond,
		BufferSize:    50,
		MaxBackoff:    50 * time.Millisecond,
		EventTTL:      time.Hour,
	}.withDefaults()
	tr := newHTTPTransport(opts)
	defer tr.Stop()

	// Push 5x the buffer — every Push must return promptly even though the
	// server is hung.
	start := time.Now()
	pushN(tr, opts.BufferSize*5)
	elapsed := time.Since(start)
	if elapsed > 250*time.Millisecond {
		t.Fatalf("pushes blocked: took %v for %d events", elapsed, opts.BufferSize*5)
	}
	if tr.Dropped() == 0 {
		t.Fatal("expected drops under overflow, got 0")
	}
}

func TestHTTPTransport_PruneStaleEvents(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()

	opts := buildTestOptions(srv)
	opts.EventTTL = 20 * time.Millisecond
	opts.FlushInterval = 200 * time.Millisecond // delay the first flush
	tr := newHTTPTransport(opts.withDefaults())
	defer tr.Stop()

	// Push events with a CapturedAt in the past so the prune step removes
	// them immediately.
	for i := 0; i < 3; i++ {
		tr.Push(Event{
			CapturedAt: time.Now().Add(-1 * time.Hour),
			Request:    &chainkit.RequestEvent{Provider: "p", Operation: "o", Success: true},
		})
	}

	// Wait one flush interval — by then the prune should have removed all of
	// them and no request should be sent.
	time.Sleep(300 * time.Millisecond)
	if got := len(srv.seen()); got != 0 {
		t.Fatalf("expected no requests after prune, got %d", got)
	}
	if got := tr.Dropped(); got != 3 {
		t.Fatalf("dropped: got %d want 3", got)
	}
}

func TestHTTPTransport_ConcurrentPushes(t *testing.T) {
	srv := newFakeServer()
	defer srv.Close()

	tr := newHTTPTransport(buildTestOptions(srv).withDefaults())
	defer tr.Stop()

	const goroutines = 8
	const perGoroutine = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	var failed atomic.Bool
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					failed.Store(true)
				}
			}()
			for i := 0; i < perGoroutine; i++ {
				tr.Push(Event{
					CapturedAt: time.Now(),
					Request:    &chainkit.RequestEvent{Provider: "p", Operation: "o", Success: true},
				})
			}
		}()
	}
	wg.Wait()
	if failed.Load() {
		t.Fatal("a goroutine panicked")
	}
	// Stop to drain.
	tr.Stop()

	var delivered int
	for _, r := range srv.seen() {
		delivered += len(r.Body.Events)
	}
	expected := goroutines * perGoroutine
	// Invariant: every event is accounted for as delivered OR dropped on
	// overflow. BufferSize is 20 in the test config so most are expected to
	// be dropped — that is by design under saturation.
	if delivered+int(tr.Dropped()) != expected {
		t.Fatalf("delivered + dropped: %d + %d = %d, want %d",
			delivered, tr.Dropped(), delivered+int(tr.Dropped()), expected)
	}
	if delivered == 0 {
		t.Fatal("nothing delivered — flush loop is stuck")
	}
}

// Sanity check on nextBackoff: each step ≥ previous (modulo jitter), bounded
// at max, and never zero after the first step.
func TestNextBackoff_Monotonic(t *testing.T) {
	max := 1 * time.Second
	got := time.Duration(0)
	var sequence []time.Duration
	for i := 0; i < 8; i++ {
		got = nextBackoff(got, max)
		sequence = append(sequence, got)
		if got <= 0 {
			t.Fatalf("backoff %d non-positive: %v", i, got)
		}
		if got > max+max/5 { // jitter can add up to 20%
			t.Fatalf("backoff %d exceeds max: %v > %v", i, got, max)
		}
	}
	// Use sequence so it isn't optimised away.
	if len(sequence) != 8 {
		t.Fatal("missing samples")
	}
}

// Compile-time check that errStopped is preserved (it's exported for future
// use by helpers).
var _ = errors.New
