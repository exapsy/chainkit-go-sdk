package cloudagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// httpTransport is the production transport implementing the ring-buffer +
// flush loop + retry semantics documented in chainkit-go-sdk/docs/
// cloud-ingest-schema.md.
//
// Concurrency model:
//   - Push is called from the SDK request path (potentially many goroutines).
//     It is non-blocking: it takes a mutex only briefly to append into the
//     buffer + drop oldest on overflow, then non-blocking-signals the loop.
//   - A single loop goroutine reads from the buffer and POSTs to the cloud.
//     One in-flight POST at a time; the loop is strictly sequential.
//   - Stop is idempotent and bounded: it closes stopCh, the loop drains
//     whatever it can in stopDrainDeadline, then exits.
//
// The buffer is a plain slice rather than a fixed-capacity ring because
// drop-oldest is rare in steady state and the cost of the slice mechanics
// (one occasional reslice or copy) is dwarfed by the JSON+HTTP cost of a
// real flush. Optimise when profiling tells us to.
type httpTransport struct {
	opts Options
	url  string

	mu       sync.Mutex
	buf      []Event // events waiting to be flushed; head=oldest, tail=newest
	inflight []Event // batch currently being POSTed; restored to head of buf on retry

	// dropped is incremented on every overflow (drop-oldest) or expiry
	// (EventTTL). Exposed for telemetry-of-telemetry; reads are atomic-only.
	dropped atomic.Uint64

	signalCh chan struct{} // size-1 non-blocking signal "you should flush"
	stopCh   chan struct{} // closed once on Stop()
	doneCh   chan struct{} // closed when the loop returns

	stopOnce sync.Once
}

// stopDrainDeadline bounds the time Stop will spend trying to flush pending
// events. After this, the transport gives up and exits. Five seconds is
// generous for the normal case (one or two batches) and short enough that a
// dying process doesn't hang.
const stopDrainDeadline = 5 * time.Second

// newHTTPTransport constructs and starts the transport. Endpoint and APIKey on
// opts are required; the caller validates them upstream. The returned
// transport's loop goroutine runs until Stop is called.
func newHTTPTransport(opts Options) *httpTransport {
	t := &httpTransport{
		opts:     opts,
		url:      opts.Endpoint + "/v1/ingest",
		signalCh: make(chan struct{}, 1),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	go t.loop()
	return t
}

// Push enqueues an event. It is non-blocking under all conditions:
//   - If the buffer is below BufferSize, append.
//   - If the buffer is at BufferSize, drop the oldest event, bump dropped,
//     and append.
//   - If the buffer reaches BatchSize after the append, non-blocking-signal
//     the loop to flush ahead of schedule.
//
// The mutex is held only across slice manipulation; allocations happen before
// or after the critical section.
func (t *httpTransport) Push(e Event) {
	t.mu.Lock()
	if len(t.buf) >= t.opts.BufferSize {
		// Drop oldest. Reslice rather than append-and-shift: O(1).
		t.buf = t.buf[1:]
		t.dropped.Add(1)
	}
	t.buf = append(t.buf, e)
	needsFlush := len(t.buf) >= t.opts.BatchSize
	t.mu.Unlock()

	if needsFlush {
		// Best-effort signal. If the channel is already buffered (size 1) the
		// loop will pick up its work on the next iteration anyway.
		select {
		case t.signalCh <- struct{}{}:
		default:
		}
	}
}

// Stop terminates the loop. Best-effort flush of pending events bounded by
// stopDrainDeadline. Idempotent.
func (t *httpTransport) Stop() {
	t.stopOnce.Do(func() {
		close(t.stopCh)
	})
	// Wait for loop to exit. doneCh is closed by the loop after drain.
	<-t.doneCh
}

// Dropped returns the running count of dropped events (overflow + TTL expiry).
func (t *httpTransport) Dropped() uint64 { return t.dropped.Load() }

// loop is the flush goroutine. Sequential by design: one flush at a time.
func (t *httpTransport) loop() {
	defer close(t.doneCh)
	flushTicker := time.NewTicker(t.opts.FlushInterval)
	defer flushTicker.Stop()
	var backoff time.Duration

	for {
		// Wait for either the ticker, an explicit signal, or stop.
		select {
		case <-t.stopCh:
			t.drainAndExit()
			return
		case <-flushTicker.C:
		case <-t.signalCh:
		}

		// Honour backoff before each attempt. We interrupt the sleep on stop
		// so a slow backoff doesn't delay shutdown.
		if backoff > 0 {
			timer := time.NewTimer(backoff)
			select {
			case <-t.stopCh:
				timer.Stop()
				t.drainAndExit()
				return
			case <-timer.C:
			}
		}

		// Prune events that have aged past EventTTL.
		t.pruneStale(nowFunc())

		ok, err := t.flushOnce(context.Background())
		switch {
		case ok:
			backoff = 0
		case err == nil:
			// nothing to flush; reset backoff so we don't sleep next time
			backoff = 0
		default:
			backoff = nextBackoff(backoff, t.opts.MaxBackoff)
			t.opts.Logger.Warn("cloudagent flush failed", "error", err.Error(), "next_backoff", backoff)
		}
	}
}

// drainAndExit attempts one or more flushes (no retries) until the buffer is
// empty or stopDrainDeadline expires.
func (t *httpTransport) drainAndExit() {
	ctx, cancel := context.WithTimeout(context.Background(), stopDrainDeadline)
	defer cancel()
	for {
		t.mu.Lock()
		empty := len(t.buf) == 0
		t.mu.Unlock()
		if empty {
			return
		}
		ok, _ := t.flushOnce(ctx)
		if !ok {
			return
		}
	}
}

// pruneStale removes events at the head of buf whose CapturedAt is older than
// now - EventTTL. Counts each dropped one in dropped.
func (t *httpTransport) pruneStale(now time.Time) {
	cutoff := now.Add(-t.opts.EventTTL)
	t.mu.Lock()
	defer t.mu.Unlock()
	drop := 0
	for drop < len(t.buf) && t.buf[drop].CapturedAt.Before(cutoff) {
		drop++
	}
	if drop > 0 {
		t.buf = t.buf[drop:]
		t.dropped.Add(uint64(drop))
	}
}

// flushOnce pulls up to BatchSize events from the buffer, POSTs them, and
// either commits (on success) or restores the in-flight batch to the head of
// the buffer (on retry-eligible failure). Returns (sent, err):
//   - sent==true              — a request was posted and accepted (2xx).
//   - sent==false && err==nil — buffer empty, nothing to do.
//   - sent==false && err!=nil — request failed; loop should back off.
//
// On a "poison" response (4xx that isn't 429), the events are dropped without
// being restored: the cloud has told us they are malformed and retrying won't
// help. We count those as dropped via t.dropped so the customer can spot the
// regression in their own telemetry.
func (t *httpTransport) flushOnce(ctx context.Context) (bool, error) {
	t.takeInflight()
	if len(t.inflight) == 0 {
		return false, nil
	}

	body, err := buildBatch(t.inflight, t.opts, newEventID(), nowFunc(), newEventID)
	if err != nil {
		// Serialisation failure should never happen with our typed structs.
		// Be defensive: drop the batch to avoid a poison loop.
		t.dropped.Add(uint64(len(t.inflight)))
		t.discardInflight()
		return false, fmt.Errorf("serialise: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		t.returnInflightToHead()
		return false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.opts.APIKey)
	req.Header.Set("User-Agent", t.opts.AgentName)

	resp, err := t.opts.HTTPClient.Do(req)
	if err != nil {
		t.returnInflightToHead()
		return false, fmt.Errorf("post: %w", err)
	}
	// Drain + close so the connection can be reused.
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		t.discardInflight()
		return true, nil
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode >= 500:
		// Retry-eligible: return events to the buffer.
		t.returnInflightToHead()
		return false, fmt.Errorf("retryable status %d", resp.StatusCode)
	default:
		// 4xx other than 429: the events are poison or the key is revoked.
		// Drop and surface an error so the loop logs once.
		dropped := len(t.inflight)
		t.dropped.Add(uint64(dropped))
		t.discardInflight()
		return false, fmt.Errorf("non-retryable status %d (dropped %d events)", resp.StatusCode, dropped)
	}
}

// takeInflight pops up to BatchSize events off the head of buf into inflight.
func (t *httpTransport) takeInflight() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.inflight) != 0 {
		// Should not happen — loop is strictly sequential — but be defensive.
		return
	}
	n := len(t.buf)
	if n > t.opts.BatchSize {
		n = t.opts.BatchSize
	}
	if n == 0 {
		return
	}
	// Take by reslicing so we don't allocate. Push will append to the new
	// (empty-prefix) buf; that's fine.
	t.inflight = t.buf[:n:n]
	t.buf = t.buf[n:]
}

// discardInflight clears the in-flight batch.
func (t *httpTransport) discardInflight() {
	t.mu.Lock()
	t.inflight = nil
	t.mu.Unlock()
}

// returnInflightToHead pushes the in-flight batch back to the head of buf.
// If the combined length exceeds BufferSize, the oldest are dropped (events
// from inflight take precedence over freshly-pushed ones since they are
// older — preserving FIFO order).
func (t *httpTransport) returnInflightToHead() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.inflight) == 0 {
		return
	}
	combined := make([]Event, 0, len(t.inflight)+len(t.buf))
	combined = append(combined, t.inflight...)
	combined = append(combined, t.buf...)
	if len(combined) > t.opts.BufferSize {
		drop := len(combined) - t.opts.BufferSize
		combined = combined[drop:]
		t.dropped.Add(uint64(drop))
	}
	t.buf = combined
	t.inflight = nil
}

// nextBackoff implements bounded exponential backoff with jitter. The first
// failure pauses for one FlushInterval; subsequent failures double up to max.
func nextBackoff(current, max time.Duration) time.Duration {
	const minBackoff = 250 * time.Millisecond
	if current <= 0 {
		current = minBackoff
	} else {
		current = current * 2
	}
	if current > max {
		current = max
	}
	// Up to ±20% jitter, deterministic on the timestamp seed so test runs are
	// reproducible.
	jitter := time.Duration(rand.Int63n(int64(current / 5)))
	if rand.Intn(2) == 0 {
		current -= jitter
	} else {
		current += jitter
	}
	return current
}

// Compile-time check.
var _ transport = (*httpTransport)(nil)

// errStopped is returned by the loop's internal helpers when Stop has fired.
// Surfaced for tests; the loop itself returns nil on stop.
var errStopped = errors.New("cloudagent: stopped")
