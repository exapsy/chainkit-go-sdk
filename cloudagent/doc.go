// Package cloudagent integrates a chainkit SDK process with chainkit-cloud, the
// observability and remote-configuration SaaS for chainkit users.
//
// # What it does
//
// Two flows, both opt-in:
//
//  1. Telemetry push: a [MetricsRecorder] and [ScoringRecorder] capture per-operation
//     and per-provider scoring events from the SDK and ship them, batched, to
//     chainkit-cloud.
//  2. Remote configuration pull: a [ConfigPoller] periodically fetches the desired
//     runtime configuration (per-chain ChainConfig, selection strategies, scoring
//     weights, scoring on/off) from chainkit-cloud and applies it to a live
//     [chainkit.ConfigUpdater] without restarting the process.
//
// What it deliberately does not do: the cloud is never in the request path. The SDK
// continues to talk to upstream providers (Mempool, Blockstream, Tatum, ...) directly.
// If chainkit-cloud is unreachable, blockchain operations are unaffected — telemetry
// is buffered and dropped when the buffer fills, and remote-config polling backs off
// silently.
//
// # Wiring
//
//	import (
//	    "github.com/exapsy/chainkit"
//	    "github.com/exapsy/chainkit/cloudagent"
//	)
//
//	opts := cloudagent.Options{
//	    Endpoint: "https://api.chainkit.cloud",
//	    APIKey:   "ck_live_...",
//	}
//
//	rec := cloudagent.NewMetricsRecorder(opts)
//	client := chainkit.NewMixedProvidersBuilder().
//	    WithMetricsRecorder(rec).
//	    // ... provider chains ...
//	    Build()
//
//	poller := cloudagent.NewConfigPoller(client, opts)
//	poller.Start(ctx)
//	defer poller.Stop()
//
// # Status
//
// This package is a skeleton in the chainkit-cloud P0 milestone: the public API is
// stable, but the underlying transport is a no-op that records events in memory.
// The real ring-buffer-backed HTTP transport ships in chainkit-cloud P2 along with
// the cloud-srv ingest endpoint.
package cloudagent
