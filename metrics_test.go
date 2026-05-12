package chainkit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// captureRecorder implements MetricsRecorder. It records every plain call.
type captureRecorder struct {
	mu    sync.Mutex
	plain []plainCall
}

type plainCall struct {
	provider, operation string
	success             bool
	duration            time.Duration
}

func (c *captureRecorder) RecordBlockchainRequest(_ context.Context, provider, operation string, success bool, duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plain = append(c.plain, plainCall{provider, operation, success, duration})
}

// captureRichRecorder implements both MetricsRecorder and RichMetricsRecorder.
// We track which method was called so the SDK's type-assertion behaviour can
// be verified.
type captureRichRecorder struct {
	captureRecorder
	rich []RequestEvent
}

func (c *captureRichRecorder) RecordBlockchainRequestRich(_ context.Context, e RequestEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rich = append(c.rich, e)
}

// newTestPM constructs a providerManager with a single fake provider, no retries,
// and a no-op selection strategy. The returned op closure controls success/failure.
func newTestPM(t *testing.T, name string) *providerManager {
	t.Helper()
	cfg := DefaultChainConfig(ProviderChainBalanceFetchers)
	cfg.RetryPolicy.Enabled = false
	cfg.RetryPolicy.MaxAttempts = 1
	cfg.CircuitBreaker.Enabled = false
	pm := newProviderManager(cfg)
	pm.addProvider(struct{}{}, 1, name)
	return pm
}

func TestExecuteWithRichMetricsRecorder_Upcasts(t *testing.T) {
	pm := newTestPM(t, "p1")
	rec := &captureRichRecorder{}

	got, err := executeWithFallbackAndMetrics(context.Background(), pm, rec, "TestOp",
		func(_ any) (string, error) {
			time.Sleep(2 * time.Millisecond)
			return "ok", nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("got %q, want %q", got, "ok")
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.rich) != 1 {
		t.Fatalf("expected 1 rich call, got %d (plain=%d)", len(rec.rich), len(rec.plain))
	}
	if len(rec.plain) != 0 {
		t.Fatalf("expected 0 plain calls when rich is satisfied, got %d", len(rec.plain))
	}
	ev := rec.rich[0]
	if ev.Provider != "p1" || ev.Operation != "TestOp" || !ev.Success {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if ev.Duration <= 0 {
		t.Fatalf("expected non-zero duration, got %v", ev.Duration)
	}
	if ev.AttemptCount != 1 {
		t.Fatalf("expected 1 attempt, got %d", ev.AttemptCount)
	}
	if ev.ErrorClass != "" {
		t.Fatalf("expected empty ErrorClass on success, got %q", ev.ErrorClass)
	}
}

func TestExecuteWithPlainMetricsRecorder_FallsBack(t *testing.T) {
	pm := newTestPM(t, "p1")
	rec := &captureRecorder{}

	_, err := executeWithFallbackAndMetrics(context.Background(), pm, rec, "TestOp",
		func(_ any) (string, error) {
			time.Sleep(2 * time.Millisecond)
			return "ok", nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.plain) != 1 {
		t.Fatalf("expected 1 plain call, got %d", len(rec.plain))
	}
	call := rec.plain[0]
	if call.duration <= 0 {
		t.Fatalf("plain recorder must receive non-zero duration (A1 fix), got %v", call.duration)
	}
	if call.provider != "p1" || call.operation != "TestOp" || !call.success {
		t.Fatalf("unexpected call: %+v", call)
	}
}

func TestExecute_NoOpRecorderDoesNotPanic(t *testing.T) {
	pm := newTestPM(t, "p1")
	_, err := executeWithFallbackAndMetrics(context.Background(), pm, &NoOpMetricsRecorder{}, "TestOp",
		func(_ any) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunOp_AttemptsCountedAcrossRetries(t *testing.T) {
	cfg := DefaultChainConfig(ProviderChainBalanceFetchers)
	cfg.RetryPolicy.Enabled = true
	cfg.RetryPolicy.MaxAttempts = 3
	cfg.RetryPolicy.InitialDelay = 1 * time.Millisecond
	cfg.RetryPolicy.MaxDelay = 1 * time.Millisecond
	cfg.RetryPolicy.BackoffMultiplier = 1.0
	cfg.RetryPolicy.Jitter = false
	cfg.CircuitBreaker.Enabled = false
	pm := newProviderManager(cfg)
	pm.addProvider(struct{}{}, 1, "p1")

	var calls atomic.Int32
	rec := &captureRichRecorder{}
	_, err := executeWithFallbackAndMetrics(context.Background(), pm, rec, "TestOp",
		func(_ any) (string, error) {
			n := calls.Add(1)
			if n < 3 {
				return "", errors.New("boom")
			}
			return "ok", nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.rich) != 1 {
		t.Fatalf("expected 1 rich call, got %d", len(rec.rich))
	}
	if rec.rich[0].AttemptCount != 3 {
		t.Fatalf("expected AttemptCount=3 (2 fails + 1 success), got %d", rec.rich[0].AttemptCount)
	}
}

func TestRunOp_AttemptsCountedAcrossProviderFallback(t *testing.T) {
	cfg := DefaultChainConfig(ProviderChainBalanceFetchers)
	cfg.RetryPolicy.Enabled = false
	cfg.RetryPolicy.MaxAttempts = 1
	cfg.CircuitBreaker.Enabled = false
	pm := newProviderManager(cfg)
	pm.addProvider("provider-a", 1, "a")
	pm.addProvider("provider-b", 2, "b")

	rec := &captureRichRecorder{}
	got, err := executeWithFallbackAndMetrics(context.Background(), pm, rec, "TestOp",
		func(p any) (string, error) {
			if p.(string) == "provider-a" {
				return "", errors.New("a-down")
			}
			return "served-by-b", nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "served-by-b" {
		t.Fatalf("got %q, want served-by-b", got)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.rich) != 1 {
		t.Fatalf("expected 1 rich call, got %d", len(rec.rich))
	}
	ev := rec.rich[0]
	if ev.Provider != "b" {
		t.Fatalf("expected winning provider 'b', got %q", ev.Provider)
	}
	if ev.AttemptCount != 2 {
		t.Fatalf("expected AttemptCount=2 (a + b), got %d", ev.AttemptCount)
	}
	if ev.ErrorClass != "" {
		t.Fatalf("expected empty ErrorClass on success, got %q", ev.ErrorClass)
	}
}

func TestRunOp_AllProvidersFailReportsTerminalError(t *testing.T) {
	cfg := DefaultChainConfig(ProviderChainBalanceFetchers)
	cfg.RetryPolicy.Enabled = false
	cfg.RetryPolicy.MaxAttempts = 1
	cfg.CircuitBreaker.Enabled = false
	pm := newProviderManager(cfg)
	pm.addProvider(struct{}{}, 1, "p1")

	rec := &captureRichRecorder{}
	authErr := fmt.Errorf("creds rejected: %w", ErrAuthFailure)
	_, err := executeWithFallbackAndMetrics(context.Background(), pm, rec, "TestOp",
		func(_ any) (string, error) { return "", authErr })
	if err == nil {
		t.Fatal("expected error from failed provider")
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.rich) != 1 {
		t.Fatalf("expected 1 rich call, got %d", len(rec.rich))
	}
	ev := rec.rich[0]
	if ev.Success {
		t.Fatal("expected Success=false")
	}
	if ev.ErrorClass != "auth" {
		t.Fatalf("expected ErrorClass=auth, got %q", ev.ErrorClass)
	}
	if ev.AttemptCount != 1 {
		t.Fatalf("expected AttemptCount=1, got %d", ev.AttemptCount)
	}
}

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"auth-wrapped", fmt.Errorf("wrapped: %w", ErrAuthFailure), "auth"},
		{"config", ErrProviderNotConfigured, "config"},
		{"deadline", context.DeadlineExceeded, "timeout"},
		{"canceled", context.Canceled, "timeout"},
		{"rate-limit-text", errors.New("rate limit exceeded"), "rate_limit"},
		{"http-429-text", errors.New("got 429 response"), "rate_limit"},
		{"too-many-requests", errors.New("Too Many Requests"), "rate_limit"},
		{"other", errors.New("kaboom"), "other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyError(tc.err); got != tc.want {
				t.Fatalf("classifyError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
