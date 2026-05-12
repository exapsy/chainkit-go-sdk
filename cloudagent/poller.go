package cloudagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/exapsy/chainkit"
)

// ConfigPoller fetches the desired runtime configuration from chainkit-cloud and
// applies it to a chainkit.ConfigUpdater (typically *chainkit.MixedProviders).
//
// In the P0 skeleton, the poller has the full lifecycle (Start/Stop, periodic
// fetch loop, exponential backoff) but the fetch step itself is a no-op until
// the cloud-srv config endpoint ships in chainkit-cloud P3. Tests can override
// the fetch hook to verify polling, application, and backoff behaviour.
type ConfigPoller struct {
	updater chainkit.ConfigUpdater
	opts    Options

	// fetch is the hook that retrieves the next desired config and the new ETag.
	// The default implementation is a no-op; tests and the P3 transport replace it.
	fetch fetchFn

	// applied tracks the most recently applied snapshot — used for diff/no-op.
	applied chainkit.ConfigSnapshot
	etag    string

	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
}

// fetchResult is what fetch returns. notModified=true signals "no change since etag".
type fetchResult struct {
	snap        chainkit.ConfigSnapshot
	etag        string
	notModified bool
}

type fetchFn func(ctx context.Context, etag string) (fetchResult, error)

// NewConfigPoller wires a ConfigPoller against a *chainkit.MixedProviders (or any
// other implementation of chainkit.ConfigUpdater). Call Start to begin polling
// and Stop to terminate the loop.
//
// When opts.Endpoint is empty the fetch is a no-op — the poller cycles but
// never changes config. This keeps dev/tests free of a required cloud target.
func NewConfigPoller(updater chainkit.ConfigUpdater, opts Options) *ConfigPoller {
	opts = opts.withDefaults()
	return newConfigPoller(updater, opts, makeFetcher(opts))
}

// newConfigPoller is the internal constructor that lets tests inject a fetch hook.
func newConfigPoller(updater chainkit.ConfigUpdater, opts Options, fetch fetchFn) *ConfigPoller {
	if fetch == nil {
		fetch = noopFetch
	}
	return &ConfigPoller{updater: updater, opts: opts, fetch: fetch}
}

// noopFetch is used when no Endpoint is configured.
func noopFetch(_ context.Context, etag string) (fetchResult, error) {
	return fetchResult{etag: etag, notModified: true}, nil
}

// makeFetcher returns the production fetcher closing over opts. The fetcher
// speaks HTTP+JSON against {Endpoint}/v1/config and honours If-None-Match for
// 304 short-circuits.
//
// Wire shape (matches services/config.Snapshot in chainkit-cloud-srv):
//   { "version": int, "etag": "\"...\"", "config": { ... } }
// where "config" is the JSON form of chainkit.ConfigSnapshot.
func makeFetcher(opts Options) fetchFn {
	if opts.Endpoint == "" {
		return noopFetch
	}
	url := opts.Endpoint + "/v1/config"
	return func(ctx context.Context, etag string) (fetchResult, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fetchResult{}, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+opts.APIKey)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", opts.AgentName)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}

		resp, err := opts.HTTPClient.Do(req)
		if err != nil {
			return fetchResult{}, fmt.Errorf("get config: %w", err)
		}
		defer func() {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}()

		switch {
		case resp.StatusCode == http.StatusNotModified:
			return fetchResult{etag: etag, notModified: true}, nil
		case resp.StatusCode == http.StatusNotFound:
			// Project has no saved config yet — treat as "no change," not an
			// error. The agent will keep polling and pick it up when the
			// dashboard saves one.
			return fetchResult{etag: etag, notModified: true}, nil
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			// fall through
		default:
			return fetchResult{}, fmt.Errorf("config endpoint returned %d", resp.StatusCode)
		}

		var envelope struct {
			Version int                      `json:"version"`
			ETag    string                   `json:"etag"`
			Config  chainkit.ConfigSnapshot  `json:"config"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
			return fetchResult{}, fmt.Errorf("decode config: %w", err)
		}
		// Prefer the server's ETag header so we round-trip the exact bytes the
		// server emits; envelope.ETag is a fallback when the header is missing
		// (some proxies strip ETag).
		serverETag := resp.Header.Get("ETag")
		if serverETag == "" {
			serverETag = envelope.ETag
		}
		return fetchResult{snap: envelope.Config, etag: serverETag}, nil
	}
}

// Start launches the polling loop. It is safe to call multiple times — subsequent
// calls are no-ops until Stop is called. The supplied ctx bounds the loop's
// lifetime; canceling ctx terminates the goroutine even without an explicit Stop.
func (p *ConfigPoller) Start(ctx context.Context) {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.running = true
	p.mu.Unlock()

	go p.loop(loopCtx)
}

// Stop terminates the polling loop. Safe to call before Start (no-op) and to call
// multiple times.
func (p *ConfigPoller) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running {
		return
	}
	p.cancel()
	p.running = false
}

// LastApplied returns a copy of the last successfully applied snapshot. Useful
// for dashboards reporting "what config is the SDK running."
func (p *ConfigPoller) LastApplied() chainkit.ConfigSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.applied
}

// loop is the polling goroutine. It alternates between sleep-PollInterval and
// fetch+apply, with exponential backoff on errors capped at MaxBackoff.
func (p *ConfigPoller) loop(ctx context.Context) {
	backoff := p.opts.PollInterval
	for {
		// Wait for the next tick or for context cancellation.
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		err := p.tick(ctx)
		switch {
		case err == nil:
			backoff = p.opts.PollInterval // reset on success
		case errors.Is(err, context.Canceled):
			return
		default:
			// Exponential backoff on errors.
			backoff *= 2
			if backoff > p.opts.MaxBackoff {
				backoff = p.opts.MaxBackoff
			}
			p.opts.Logger.Warn("cloudagent config poll failed", "error", err.Error(), "next_backoff", backoff)
		}
	}
}

// tick performs a single fetch-and-apply cycle. Exposed for tests; the production
// loop calls it on a schedule.
func (p *ConfigPoller) tick(ctx context.Context) error {
	p.mu.Lock()
	currentETag := p.etag
	p.mu.Unlock()

	res, err := p.fetch(ctx, currentETag)
	if err != nil {
		return err
	}
	if res.notModified {
		return nil
	}

	if err := p.applySnapshot(res.snap); err != nil {
		return err
	}
	p.mu.Lock()
	p.applied = res.snap
	p.etag = res.etag
	p.mu.Unlock()
	return nil
}

// applySnapshot pushes a desired-state snapshot into the underlying ConfigUpdater.
// On per-method failure, application continues for the other methods — the poller
// favours partial application over an all-or-nothing reapply, since a transient
// failure on one chain shouldn't strand the rest.
func (p *ConfigPoller) applySnapshot(snap chainkit.ConfigSnapshot) error {
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	for chain, cfg := range snap.Chains {
		record(p.updater.UpdateChainConfig(chain, cfg))
	}
	// Scoring updates are best-effort — they no-op when scoring isn't configured.
	record(p.updater.SetScoringWeights(snap.ScoringWeights))
	p.updater.SetScoringEnabled(snap.ScoringEnabled)

	return firstErr
}
