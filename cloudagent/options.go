package cloudagent

import (
	"log/slog"
	"net/http"
	"time"
)

// Default values for Options. Exported so callers and tests can refer to them.
const (
	DefaultBatchSize     = 200
	DefaultFlushInterval = 5 * time.Second
	DefaultBufferSize    = 10_000
	DefaultPollInterval  = 30 * time.Second
	DefaultMaxBackoff    = 5 * time.Minute
	DefaultEventTTL      = 10 * time.Minute
	DefaultSampleRate    = 1.0
	DefaultChain         = "btc"
	DefaultNetwork       = "mainnet"
)

// Options configures a cloudagent. The zero value is not usable — at minimum
// Endpoint and APIKey must be set. All other fields fall back to documented defaults.
type Options struct {
	// Endpoint is the base URL of the chainkit-cloud API (e.g.
	// "https://api.chainkit.cloud"). Required.
	Endpoint string

	// APIKey is the project-scoped API key issued by chainkit-cloud. Required.
	APIKey string

	// BatchSize is the maximum number of events bundled into a single ingest request.
	// When the buffer holds at least BatchSize events, a flush is triggered ahead of
	// the FlushInterval timer. Defaults to DefaultBatchSize.
	BatchSize int

	// FlushInterval is how often the agent flushes any accumulated events even when
	// the buffer is below BatchSize. Defaults to DefaultFlushInterval.
	FlushInterval time.Duration

	// BufferSize is the maximum number of buffered events held in memory. When the
	// buffer is full, the oldest event is dropped (drop-oldest semantics) so the
	// SDK call site is never blocked. Defaults to DefaultBufferSize.
	BufferSize int

	// SampleRate is the fraction of events forwarded to the cloud, in [0, 1]. 1.0
	// (the default) forwards every event. Lower values reduce telemetry volume on
	// high-throughput services; the agent samples uniformly at random. Defaults to
	// DefaultSampleRate.
	SampleRate float64

	// PollInterval is how often the ConfigPoller fetches the latest config from the
	// cloud. Defaults to DefaultPollInterval.
	PollInterval time.Duration

	// MaxBackoff is the upper bound on the exponential backoff applied when the cloud
	// is unreachable or returns 5xx. Defaults to DefaultMaxBackoff.
	MaxBackoff time.Duration

	// EventTTL is the maximum age an event can sit in the buffer before being dropped.
	// Stale events past EventTTL are discarded on the next flush. Defaults to
	// DefaultEventTTL.
	EventTTL time.Duration

	// Chain labels every outgoing telemetry event with the blockchain family
	// the customer's SDK is talking to. Defaults to "btc". This lives on the
	// cloudagent rather than on the SDK's RequestEvent because the chain is a
	// deployment-level fact, not a per-call value.
	Chain string

	// Network labels every outgoing event with the network within Chain
	// (e.g. "mainnet", "testnet", "signet"). Defaults to "mainnet".
	Network string

	// AgentName identifies the SDK + agent version in the batch envelope's
	// agent field. Defaults to "chainkit-go-sdk/dev cloudagent/v1". Useful for
	// support triage of version-specific bugs.
	AgentName string

	// HTTPClient is the underlying HTTP client. Tests may override to inject a
	// transport. When nil, a default *http.Client with a 10s timeout is used.
	HTTPClient *http.Client

	// Logger is the optional structured logger. When nil, slog.Default is used.
	Logger *slog.Logger
}

// withDefaults returns a copy of opts with unset fields populated from the
// Default* constants. Required fields (Endpoint, APIKey) are not validated here —
// callers handle them.
func (o Options) withDefaults() Options {
	if o.BatchSize <= 0 {
		o.BatchSize = DefaultBatchSize
	}
	if o.FlushInterval <= 0 {
		o.FlushInterval = DefaultFlushInterval
	}
	if o.BufferSize <= 0 {
		o.BufferSize = DefaultBufferSize
	}
	if o.SampleRate <= 0 {
		o.SampleRate = DefaultSampleRate
	}
	if o.PollInterval <= 0 {
		o.PollInterval = DefaultPollInterval
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = DefaultMaxBackoff
	}
	if o.EventTTL <= 0 {
		o.EventTTL = DefaultEventTTL
	}
	if o.Chain == "" {
		o.Chain = DefaultChain
	}
	if o.Network == "" {
		o.Network = DefaultNetwork
	}
	if o.AgentName == "" {
		o.AgentName = "chainkit-go-sdk/dev cloudagent/v1"
	}
	if o.HTTPClient == nil {
		// A fresh client (not http.DefaultClient) so the 10s timeout doesn't
		// bleed onto unrelated callers in the same process.
		o.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return o
}
