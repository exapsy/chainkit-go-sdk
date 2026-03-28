# chainkit

A chain-agnostic, multi-provider blockchain client library for Go.

chainkit gives you a single interface to multiple blockchain data providers with automatic fallback, circuit breaking, rate limiting, and provider selection strategies — so your application keeps working when any individual provider is down or rate-limited.

Bitcoin is the first supported chain. The architecture is designed to add Ethereum and other chains without breaking existing code.

## Features

- **Multi-provider fallback** — register N providers per role; chainkit tries them in priority order until one succeeds
- **Circuit breaker** — opens automatically after repeated failures, re-checks on a configurable timeout
- **Rate limiting** — per-chain request rate limiting with burst support
- **Retry with backoff** — exponential backoff with optional jitter
- **Provider selection strategies** — priority-only, round-robin, random, least-loaded
- **Metrics hook** — inject your own `MetricsRecorder` (Prometheus, Datadog, etc.)
- **Health checks** — optional periodic health probing per provider chain
- **Zero required roles** — register only the capabilities you use; unregistered roles return `ErrProviderNotConfigured`

## Installation

```
go get github.com/exapsy/chainkit@latest
```

Requires Go 1.22+.

## Quick start

```go
import (
    "github.com/exapsy/chainkit"
    "github.com/exapsy/chainkit/bitcoin/providers"
    "github.com/exapsy/chainkit/bitcoin/types"
)

metal := providers.NewMetal(types.BitcoinNetworkMainnet)

mempool, err := providers.NewMempool(types.BitcoinNetworkMainnet, "https://mempool.space/api")
if err != nil {
    return err
}

client, err := chainkit.NewMixedProvidersBuilder().
    WithAddressGeneratorChain(chainkit.AddressGeneratorConfig{Generator: metal, Priority: 1}).
    WithTxAssemblerChain(chainkit.TxAssemblerConfig{Assembler: metal, Priority: 1}).
    WithTxSignerChain(chainkit.TxSignerConfig{Signer: metal, Priority: 1}).
    WithTxSizerChain(chainkit.TxSizerConfig{Sizer: metal, Priority: 1}).
    WithFeeEstimatorChain(chainkit.FeeEstimatorConfig{Estimator: metal, Priority: 1}).
    WithFeeRecommenderChain(chainkit.FeeRecommenderConfig{Recommender: mempool, Priority: 1}).
    WithBalanceFetcherChain(chainkit.BalanceFetcherConfig{Fetcher: mempool, Priority: 1}).
    WithUTXOFetcherChain(chainkit.UTXOFetcherConfig{Fetcher: mempool, Priority: 1}).
    WithRateFetcherChain(chainkit.RateFetcherConfig{Fetcher: mempool, Priority: 1}).
    WithTxBroadcasterChain(chainkit.TxBroadcasterConfig{Broadcaster: mempool, Priority: 1}).
    WithTxStatusFetcher(mempool).
    Build()
if err != nil {
    return err
}

// Get UTXOs
utxos, err := client.GetUTXOs(ctx, "bc1q...")

// Get balance
balance, err := client.GetBalance(ctx, "bc1q...", nil)

// Get fee recommendations
fees, err := client.GetTxFees(ctx)
```

## Multi-provider fallback example

Register multiple providers for the same role. chainkit tries them in priority order (lower number = higher priority) and falls back automatically.

```go
client, err := chainkit.NewMixedProvidersBuilder().
    WithBalanceFetcherChain(
        chainkit.BalanceFetcherConfig{Fetcher: mempool,     Priority: 1},
        chainkit.BalanceFetcherConfig{Fetcher: blockcypher, Priority: 2},
        chainkit.BalanceFetcherConfig{Fetcher: blockstream, Priority: 3},
    ).
    Build()
```

## Custom chain configuration

Override circuit breaker, retry, rate limit, and selection strategy per role:

```go
import "time"

chainCfg := chainkit.ChainConfig{
    RetryPolicy: chainkit.RetryPolicy{
        Enabled:           true,
        MaxAttempts:       3,
        InitialDelay:      200 * time.Millisecond,
        MaxDelay:          10 * time.Second,
        BackoffMultiplier: 2.0,
        Jitter:            true,
    },
    CircuitBreaker: chainkit.CircuitBreakerConfig{
        Enabled:          true,
        FailureThreshold: 5,
        SuccessThreshold: 2,
        Timeout:          30 * time.Second,
    },
    SelectionStrategy: chainkit.SelectionStrategyRoundRobin,
    Timeout:           15 * time.Second,
}

client, err := chainkit.NewMixedProvidersBuilder().
    WithBalanceFetcherChain(
        chainkit.BalanceFetcherConfig{
            Fetcher:     mempool,
            Priority:    1,
            ChainConfig: &chainCfg,
        },
    ).
    Build()
```

## Provider selection strategies

| Strategy | Behaviour |
|---|---|
| `SelectionStrategyPriorityOnly` | Always try providers in priority order (default) |
| `SelectionStrategyRoundRobin` | Round-robin within same-priority providers |
| `SelectionStrategyRandom` | Random ordering within same-priority providers |
| `SelectionStrategyLeastLoaded` | Prefer providers with fewer recent failures |

## Pinning a specific provider

Use `ProviderSelector` to pin all calls to a named provider (useful for debugging or A/B testing):

```go
selector := chainkit.NewProviderSelector(client, "Mempool")
balance, err := selector.GetBalance(ctx, address, nil)
```

## Metrics

Implement `MetricsRecorder` and pass it to the builder:

```go
type prometheusRecorder struct{ ... }

func (r *prometheusRecorder) RecordBlockchainRequest(
    ctx context.Context,
    provider, operation string,
    success bool,
    duration time.Duration,
) {
    // record to your metrics system
}

client, err := chainkit.NewMixedProvidersBuilder().
    WithMetricsRecorder(&prometheusRecorder{}).
    // ...
    Build()
```

The default is `NoOpMetricsRecorder` which discards all metrics.

## Bitcoin providers

| Provider | Capabilities |
|---|---|
| `metal` | AddressGenerator, FeeEstimator, TxAssembler, TxSigner, TxSizer, AddressValidator — **local only, no network** |
| `mempool` | FeeRecommender, BalanceFetcher, UTXOFetcher, TxBroadcaster, RateFetcher, TxStatusFetcher |
| `blockcypher` | BalanceFetcher, UTXOFetcher |
| `blockstream` | UTXOFetcher, BalanceFetcher, AddressValidator, FeeRecommender |
| `bitrefcom` | TxBroadcaster, FeeRecommender, BalanceFetcher |
| `coingecko` | RateFetcher |

### Creating providers

```go
import "github.com/exapsy/chainkit/bitcoin/providers"

// metal — local key derivation, no network calls
metal := providers.NewMetal(types.BitcoinNetworkMainnet)

// mempool — mempool.space API
mempool := providers.NewMempool(types.BitcoinNetworkMainnet, "https://mempool.space/api")

// blockcypher — requires API token
blockcypher := providers.NewBlockcypher(types.BitcoinNetworkMainnet, "your-token")

// blockstream — public API, no key required
blockstream, err := providers.NewBlockstream(types.BitcoinNetworkMainnet)

// bitrefcom — requires API key
bitrefcom := providers.NewBitrefcom("your-api-key", "https://bitref.com/api")

// coingecko — public API
coingecko := providers.NewCoingecko()
```

## Payment utilities

```go
import "github.com/exapsy/chainkit/payment"

// Build a BIP-21 bitcoin: payment URI
link := payment.BuildPaymentLink(payment.BuildPaymentLinkOptions{
    Address: "bc1q...",
    Amount:  1500000, // satoshis
    Coin:    payment.CoinBTC,
})
// "bitcoin:bc1q...?amount=0.015"

// Convert satoshis to BTC
btc := payment.ConvertSatoshisToBitcoin(1500000) // 0.015
```

## HD wallet utilities

```go
import "github.com/exapsy/chainkit/bitcoin"

// Derive BIP32 index and child-index from an arbitrary string key
index, childIndex, err := bitcoin.DeriveHDIndices("some-session-id42")
```

## Error handling

When no provider is registered for a role:

```go
balance, err := client.GetBalance(ctx, address, nil)
if errors.Is(err, chainkit.ErrProviderNotConfigured) {
    // register a BalanceFetcher with WithBalanceFetcherChain
}
```

## Package structure

```
chainkit/
├── interfaces.go          # Core provider interfaces
├── config.go              # RetryPolicy, CircuitBreakerConfig, ChainConfig, etc.
├── builder.go             # MixedProvidersBuilder
├── providers.go           # MixedProviders composite implementation
├── provider_selector.go   # ProviderSelector — pins calls to a named provider
├── manager.go             # ProviderManager — circuit breaker, retry, rate limit
├── metrics.go             # MetricsRecorder interface + NoOpMetricsRecorder
├── provider_types.go      # ProviderChainType, SelectionStrategy constants
├── selection_strategy.go  # Priority, round-robin, random, least-loaded selectors
├── debug.go               # DebugInfo, ExtractDebugInfo
│
├── bitcoin/
│   ├── types/types.go     # UTXO, Tx, SignedTx, FeeTier, CoinRate, etc.
│   ├── providers/         # Bitcoin provider implementations
│   ├── derive.go          # DeriveHDIndices (BIP32)
│   ├── address.go         # ValidatePublicAddress
│   └── keys.go            # GenerateKeys (secp256k1)
│
└── payment/
    └── bitcoin.go         # BuildPaymentLink, ConvertSatoshisToBitcoin
```

## Versioning

- `v0.x.y` — unstable; interfaces may change between minors
- `v1.0.0` — stable API, once the interface is confirmed in production

## License

MIT
