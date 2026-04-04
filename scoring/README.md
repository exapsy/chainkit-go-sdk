# Scoring Package

The `scoring` package provides **dynamic provider scoring** for the chainkit library. It automatically adjusts provider priorities based on runtime performance, replacing static priority-based selection with intelligent, self-optimizing provider selection.

## Features

- **Automatic score adjustment** based on:
  - Health check results (429 rate limits, auth failures, timeouts)
  - Operation success/failure rates
  - Response time relative to peer providers
- **Gradual recovery** through configurable penalty decay
- **Relative latency tracking** comparing providers against each other
- **Zero-config defaults** with full customization via functional options
- **Thread-safe** for concurrent use

## Quick Start

### Basic Usage

```go
import (
    "github.com/exapsy/chainkit"
    "github.com/exapsy/chainkit/scoring"
    "github.com/exapsy/chainkit/bitcoin/providers"
)

// Create providers
mempool, _ := providers.NewMempool(...)
blockcypher, _ := providers.NewBlockcypher(...)
blockstream, _ := providers.NewBlockstream(...)

// Build with adaptive scoring enabled
client := chainkit.NewMixedProvidersBuilder().
    WithTxBroadcasterChain(
        chainkit.TxBroadcasterConfig{Broadcaster: mempool, Priority: 1},
        chainkit.TxBroadcasterConfig{Broadcaster: blockcypher, Priority: 2},
        chainkit.TxBroadcasterConfig{Broadcaster: blockstream, Priority: 3},
    ).
    WithAdaptiveScoring(). // Enable adaptive scoring with defaults
    Build()

// Use normally - scoring happens automatically
txID, err := client.PushTx(ctx, rawTx)

// When done, clean up background goroutines
defer client.(*chainkit.MixedProviders).StopScoring()
```

### Customized Scoring

```go
client := chainkit.NewMixedProvidersBuilder().
    WithTxBroadcasterChain(...).
    WithAdaptiveScoring(
        scoring.WithRateLimitPenalty(30.0),    // Heavier penalty for 429s
        scoring.WithDecayRate(0.2),            // Faster recovery (20% per minute)
        scoring.WithAuthFailurePenalty(100.0), // Severe penalty for auth failures
        scoring.WithLatencyWindow(200),        // Keep more latency samples
    ).
    Build()
```

## How It Works

### Score Calculation

Each provider has a **base score** derived from its initial priority:

- Priority 1 → Base score 100
- Priority 2 → Base score 90
- Priority 3 → Base score 80
- etc.

The **effective score** is calculated as:

```
EffectiveScore = BaseScore - HealthPenalty - LatencyPenalty - ErrorPenalty - RateLimitPenalty
```

Providers are selected in order of effective score (highest first).

### Event Types and Penalties

| Event                      | Default Penalty | Description                      |
| -------------------------- | --------------- | -------------------------------- |
| `EventHealthCheckFailed`   | -5 points       | Generic health check failure     |
| `EventHealthCheck429`      | -20 points      | Rate limit response (429)        |
| `EventHealthCheckAuthFail` | -50 points      | Authentication failure (401/403) |
| `EventHealthCheckTimeout`  | -10 points      | Health check timed out           |
| `EventOperationFailed`     | -3 points       | Provider operation failed        |
| `EventRateLimited`         | -20 points      | Operation was rate limited       |
| `EventOperationSuccess`    | +0.5 points     | Reduces error penalty            |

### Penalty Decay

Penalties decay over time, allowing providers to recover:

- **Default decay interval**: 1 minute
- **Default decay rate**: 10% per interval

Example: A provider with 50 points of penalty will have ~45 points after 1 minute, ~40 after 2 minutes, etc.

## Persistence

The scoring engine supports **persistent storage** to preserve scores across restarts and enable multi-instance deployments with shared state.

### Quick Start with Persistence

By default, scores are stored in memory and lost on restart. To enable persistence:

```go
import (
    "github.com/exapsy/chainkit/scoring"
    "github.com/exapsy/chainkit/scoring/store"
)

// Option 1: Explicit memory store (same as default, for clarity)
engine := scoring.NewEngine(
    scoring.WithMemoryStore(),
)

// Option 2: Use store configuration
config := store.StoreConfig{
    Type: store.StoreTypeMemory,
}
engine := scoring.NewEngine(
    scoring.WithStoreConfig(config),
)

// Option 3: Provide custom store instance
myStore := store.NewMemoryStore()
engine := scoring.NewEngine(
    scoring.WithStore(myStore),
)
```

### Store Types

#### Memory Store (Available Now)

**In-memory storage** - Fast, no external dependencies, but data is lost on restart.

```go
engine := scoring.NewEngine(scoring.WithMemoryStore())
```

✅ Best for:

- Single-instance deployments
- Development and testing
- When persistence is not required

#### Redis Store (Planned - Phase 2)

**Distributed cache** with pub/sub support for multi-instance deployments.

```go
// Coming in Phase 2
config := store.StoreConfig{
    Type: store.StoreTypeRedis,
    Redis: &store.RedisConfig{
        Addr:     "localhost:6379",
        ScoreTTL: 24 * time.Hour,
    },
}
engine := scoring.NewEngine(scoring.WithStoreConfig(config))
```

#### PostgreSQL Store (Planned - Phase 3)

**Durable database storage** with migrations for long-term persistence.

```go
// Coming in Phase 3
config := store.StoreConfig{
    Type: store.StoreTypePostgres,
    Postgres: &store.PostgresConfig{
        ConnectionString: "postgres://user:pass@localhost/chainkit",
    },
}
engine := scoring.NewEngine(scoring.WithStoreConfig(config))
```

#### Hybrid Store (Planned - Phase 4)

**Combined cache + database** for best performance and durability.

```go
// Coming in Phase 4
config := store.StoreConfig{
    Type: store.StoreTypeHybrid,
    Hybrid: &store.HybridConfig{
        Primary: store.StoreConfig{Type: store.StoreTypePostgres, ...},
        Cache:   store.StoreConfig{Type: store.StoreTypeRedis, ...},
        CacheTTL: 5 * time.Minute,
    },
}
engine := scoring.NewEngine(scoring.WithStoreConfig(config))
```

### Manual Persistence Operations

#### Save Scores

```go
ctx := context.Background()

// Save all scores to store
err := engine.SaveToStore(ctx)
if err != nil {
    log.Printf("Failed to save scores: %v", err)
}

// Save a single provider's score
err = engine.PersistScore(ctx, "provider-name")
if err != nil {
    log.Printf("Failed to persist score: %v", err)
}
```

#### Load Scores

```go
// Load scores from store (overwrites in-memory state)
err := engine.LoadFromStore(ctx)
if err != nil {
    log.Printf("Failed to load scores: %v", err)
}
```

#### Periodic Persistence

For production, persist scores periodically:

```go
ticker := time.NewTicker(1 * time.Minute)
defer ticker.Stop()

for range ticker.C {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    if err := engine.SaveToStore(ctx); err != nil {
        log.Printf("Failed to persist scores: %v", err)
    }
    cancel()
}
```

#### Graceful Shutdown

Save scores before shutting down:

```go
sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
<-sigChan

// Save scores before exit
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := engine.SaveToStore(ctx); err != nil {
    log.Printf("Warning: failed to save scores on shutdown: %v", err)
}

engine.Stop()
```

### Migration Between Stores

Migrate from memory-only to persistent storage:

```go
// Old engine with default (memory-only)
oldEngine := scoring.NewEngine()
oldEngine.RegisterProvider("provider1", 1)
// ... use engine ...

// Create new persistent store
newStore := store.NewMemoryStore() // Or Redis/Postgres in future phases

// Save old state to new store
ctx := context.Background()
oldEngine.SetStore(newStore)
err := oldEngine.SaveToStore(ctx)
if err != nil {
    log.Fatal(err)
}

// Create new engine with persistent store
newEngine := scoring.NewEngine(scoring.WithStore(newStore))
// State is preserved!
```

### Store Documentation

For detailed store documentation, see [`store/README.md`](./store/README.md).

**Implementation phases:**

- ✅ Phase 1: Core interface + Memory store
- ✅ Phase 2: Redis store
- ✅ Phase 3: PostgreSQL store
- 🚧 Phase 4: Hybrid store (planned)
- 🚧 Phase 5: Metrics & observability (planned)

### Relative Latency Scoring

The engine tracks response times across all providers and penalizes those significantly slower than peers:

1. Collects latency samples in a sliding window (default: 100 samples)
2. Calculates mean and standard deviation across all providers
3. Penalizes providers >1 standard deviation above the mean
4. Penalty = (standard deviations above threshold) × SlowResponsePenalty

## Configuration Options

### Penalty Weights

| Option                        | Default | Description                                    |
| ----------------------------- | ------- | ---------------------------------------------- |
| `WithHealthCheckPenalty(p)`   | 5.0     | Penalty for health check failures              |
| `WithRateLimitPenalty(p)`     | 20.0    | Penalty for 429 rate limit errors              |
| `WithAuthFailurePenalty(p)`   | 50.0    | Penalty for auth failures (401/403)            |
| `WithSlowResponsePenalty(p)`  | 2.0     | Penalty per std deviation of slowness          |
| `WithOperationFailPenalty(p)` | 3.0     | Penalty for operation failures                 |
| `WithTimeoutPenalty(p)`       | 10.0    | Penalty for operation timeouts                 |
| `WithSuccessBonus(b)`         | 0.5     | Bonus subtracted from error penalty on success |
| `WithMaxPenalty(m)`           | 90.0    | Maximum penalty per category                   |

### Decay Settings

| Option                 | Default   | Description                                |
| ---------------------- | --------- | ------------------------------------------ |
| `WithDecayInterval(d)` | 1 minute  | How often decay is applied                 |
| `WithDecayRate(r)`     | 0.1 (10%) | Percentage of penalty reduced per interval |

### Latency Settings

| Option                     | Default | Description                                  |
| -------------------------- | ------- | -------------------------------------------- |
| `WithLatencyWindow(n)`     | 100     | Number of latency samples per provider       |
| `WithSlowThreshold(s)`     | 1.0     | Std deviations above mean to trigger penalty |
| `WithMinLatencySamples(n)` | 10      | Min samples before latency comparison        |

### Other Settings

| Option              | Default | Description                     |
| ------------------- | ------- | ------------------------------- |
| `WithEnabled(bool)` | true    | Enable/disable adaptive scoring |

## API Reference

### Engine Methods

```go
// Create a new scoring engine
engine := scoring.NewEngine(opts ...ScoringOption)

// Start background processes (decay timer)
engine.Start(ctx context.Context)

// Stop background processes
engine.Stop()

// Register/unregister providers
engine.RegisterProvider(name string, priority int)
engine.UnregisterProvider(name string)

// Record events
engine.RecordEvent(event ScoreEvent)

// Get scores
engine.GetEffectiveScore(providerName string) float64
engine.GetSortedProviders() []string
engine.GetProviderStats(providerName string) *ProviderScoreStats
engine.GetAllProviderStats() []ProviderScoreStats

// Configuration
engine.GetConfig() ScoringConfig
engine.UpdateConfig(opts ...ScoringOption) error
engine.IsEnabled() bool
engine.SetEnabled(enabled bool)
engine.Reset()
```

### MixedProviders Methods (when scoring enabled)

```go
// Get scoring statistics
client.GetScoringStats() []scoring.ProviderScoreStats
client.GetProviderScoringStats(name string) *scoring.ProviderScoreStats

// Get sorted provider order
client.GetSortedProviders() []string

// Runtime control
client.IsScoringEnabled() bool
client.SetScoringEnabled(enabled bool)
client.ResetScoring()

// Cleanup
client.StopScoring()
```

### ProviderScoreStats

```go
type ProviderScoreStats struct {
    Name             string
    BaseScore        float64
    EffectiveScore   float64
    HealthPenalty    float64
    LatencyPenalty   float64
    ErrorPenalty     float64
    RateLimitPenalty float64
    TotalOperations  int64
    SuccessfulOps    int64
    FailedOps        int64
    SuccessRate      float64
    AverageLatency   time.Duration
    LastUpdated      time.Time
}
```

## Advanced Usage

### Manual Event Recording

For custom health check implementations:

```go
engine := client.(*chainkit.MixedProviders).GetScoringEngine()

// Record a health check result
event := scoring.ClassifyHealthCheckEvent(
    "provider-name",
    httpStatus,      // e.g., 429, 200, 500
    responseTime,    // time.Duration
    err,             // error or nil
)
engine.RecordEvent(event)

// Record an operation result
event := scoring.ClassifyOperationEvent(
    "provider-name",
    responseTime,
    err,
)
engine.RecordEvent(event)
```

### Sharing Engine Across Builders

```go
// Create a shared engine
engine := scoring.NewEngine(
    scoring.WithDecayRate(0.15),
)
engine.Start(ctx)
defer engine.Stop()

// Use in multiple builders
client1 := chainkit.NewMixedProvidersBuilder().
    WithTxBroadcasterChain(...).
    WithAdaptiveScoringEngine(engine).
    Build()

client2 := chainkit.NewMixedProvidersBuilder().
    WithBalanceFetcherChain(...).
    WithAdaptiveScoringEngine(engine).
    Build()
```

### Runtime Monitoring

```go
// Periodically log provider stats
go func() {
    ticker := time.NewTicker(1 * time.Minute)
    for range ticker.C {
        stats := client.GetScoringStats()
        for _, s := range stats {
            log.Printf("Provider %s: score=%.2f, success_rate=%.2f%%, avg_latency=%v",
                s.Name, s.EffectiveScore, s.SuccessRate*100, s.AverageLatency)
        }
    }
}()
```

## Best Practices

1. **Start with defaults** - The default configuration works well for most use cases

2. **Adjust penalties based on your tolerance**:
   - High-reliability systems: Increase penalties, decrease decay rate
   - Cost-sensitive systems: Lower penalties, faster decay to utilize cheaper providers

3. **Monitor statistics** - Use `GetScoringStats()` to understand provider behavior over time

4. **Clean up** - Always call `StopScoring()` when done to prevent goroutine leaks

5. **Consider provider costs** - Set initial priorities based on cost/quality tradeoffs; scoring will adjust based on actual performance

## Thread Safety

The scoring package is fully thread-safe:

- All Engine methods are safe for concurrent use
- The decay manager runs in a background goroutine
- Latency tracking uses internal synchronization

## Performance

Scoring overhead is minimal:

- Event recording: ~100ns per event
- Score lookup: ~50ns per provider
- Memory: ~1KB per provider + latency samples
