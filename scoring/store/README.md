# Chainkit Scoring Store

The `store` package provides persistent storage backends for the chainkit scoring engine. It enables scores to survive restarts and enables multi-instance deployments with shared state.

## Overview

The scoring engine uses the `ScoreStore` interface to persist provider scores and latency statistics. Multiple implementations are available:

- **Memory** - In-memory storage (default, single-instance only)
- **Redis** - Distributed cache (planned, Phase 2)
- **PostgreSQL** - Durable database storage (planned, Phase 3)
- **Hybrid** - Combined cache + database

## Quick Start

### Default (Memory Store)

By default, the engine uses in-memory storage with no persistence:

```go
import "github.com/exapsy/chainkit/scoring"

// No persistence - data lost on restart
engine := scoring.NewEngine()
```

### Explicit Memory Store

Explicitly configure the memory store:

```go
import (
    "github.com/exapsy/chainkit/scoring"
)

engine := scoring.NewEngine(
    scoring.WithMemoryStore(),
)
```

### Custom Store

Provide your own store implementation:

```go
import (
    "github.com/exapsy/chainkit/scoring"
    "github.com/exapsy/chainkit/scoring/store"
)

myStore := store.NewMemoryStore()

engine := scoring.NewEngine(
    scoring.WithStore(myStore),
)
```

## Store Interface

All stores implement the `ScoreStore` interface:

```go
type ScoreStore interface {
    // Provider score operations
    GetScore(ctx context.Context, providerName string) (*ProviderScoreData, error)
    SetScore(ctx context.Context, data *ProviderScoreData) error
    GetAllScores(ctx context.Context) ([]*ProviderScoreData, error)
    DeleteScore(ctx context.Context, providerName string) error

    // Batch operations
    SetScores(ctx context.Context, data []*ProviderScoreData) error

    // Latency statistics
    GetLatencyStats(ctx context.Context) (*LatencyStatsData, error)
    SetLatencyStats(ctx context.Context, data *LatencyStatsData) error

    // Lifecycle
    Close() error
    Ping(ctx context.Context) error

    // Metadata
    Name() string
}
```

## Data Model

### ProviderScoreData

Serializable representation of a provider's score:

```go
type ProviderScoreData struct {
    Name             string    `json:"name"`
    BaseScore        float64   `json:"base_score"`
    HealthPenalty    float64   `json:"health_penalty"`
    LatencyPenalty   float64   `json:"latency_penalty"`
    ErrorPenalty     float64   `json:"error_penalty"`
    RateLimitPenalty float64   `json:"rate_limit_penalty"`
    TotalOperations  int64     `json:"total_operations"`
    SuccessfulOps    int64     `json:"successful_ops"`
    FailedOps        int64     `json:"failed_ops"`
    LastUpdated      time.Time `json:"last_updated"`
    LastHealthCheck  time.Time `json:"last_health_check,omitempty"`
    LastOperation    time.Time `json:"last_operation,omitempty"`
    RecentLatencies  []int64   `json:"recent_latencies,omitempty"` // nanoseconds
}
```

### LatencyStatsData

Global latency statistics across all providers:

```go
type LatencyStatsData struct {
    ProviderSamples map[string][]int64 `json:"provider_samples"` // nanoseconds
    LastUpdated     time.Time          `json:"last_updated"`
}
```

## Store Implementations

### Memory Store

**Status:** ✅ Available (Phase 1)

In-memory storage with no persistence. Suitable for:

- Single-instance deployments
- Testing and development
- Scenarios where score persistence is not required

**Characteristics:**

- ✅ Zero configuration
- ✅ Fastest performance
- ✅ No external dependencies
- ❌ Data lost on restart
- ❌ Cannot share state between instances

**Usage:**

```go
import "github.com/exapsy/chainkit/scoring/store"

memStore := store.NewMemoryStore()

// Use with engine
engine := scoring.NewEngine(scoring.WithStore(memStore))
```

### Redis Store

**Status:** ✅ Available (Phase 2)

Distributed cache with pub/sub support. Suitable for:

- Multi-instance deployments
- Shared state across instances
- Automatic expiration (TTL)
- Real-time score synchronization

**Features:**

- ✅ Distributed locking (Lockable interface)
- ✅ Pub/sub for real-time updates (Watchable interface)
- ✅ Configurable TTL (Expirable interface)
- ✅ Connection pooling
- ✅ Batch operations via pipelining
- ✅ Custom key prefixing for multi-tenancy

**Characteristics:**

- ✅ Fast distributed storage
- ✅ Shared state across instances
- ✅ Automatic expiration (TTL)
- ✅ Real-time pub/sub updates
- ✅ Distributed locking
- ⚠️ Requires Redis server
- ⚠️ Data persistence depends on Redis configuration

**Usage:**

#### Basic Redis Store

```go
import (
    "github.com/exapsy/chainkit/scoring"
    "github.com/exapsy/chainkit/scoring/store"
)

// Option 1: Simple configuration
engine := scoring.NewEngine(
    scoring.WithRedisStore("localhost:6379"),
)

// Option 2: With authentication and TTL
engine := scoring.NewEngine(
    scoring.WithRedisStore(
        "localhost:6379",
        scoring.RedisPassword("secret"),
        scoring.RedisDB(1),
        scoring.RedisScoreTTL(24 * time.Hour),
    ),
)

// Option 3: Via StoreConfig
config := store.StoreConfig{
    Type: store.StoreTypeRedis,
    Redis: &store.RedisConfig{
        Addr:         "localhost:6379",
        Password:     "",
        DB:           0,
        ScoreTTL:     24 * time.Hour,
        PoolSize:     10,
        MinIdleConns: 2,
    },
}

redisStore, err := store.NewStore(config)
if err != nil {
    log.Fatal(err)
}

engine := scoring.NewEngine(scoring.WithStore(redisStore))
```

#### Advanced Features

**Distributed Locking:**

```go
import "time"

redisStore := engine.GetStore().(*store.RedisStore)

// Acquire lock before updating
unlock, err := redisStore.Lock(ctx, "provider1", 5*time.Second)
if err != nil {
    log.Printf("Failed to acquire lock: %v", err)
    return
}
defer unlock()

// Update score while holding lock
data := &store.ProviderScoreData{
    Name:      "provider1",
    BaseScore: 100.0,
}
redisStore.SetScore(ctx, data)
```

**Pub/Sub for Real-Time Updates:**

```go
// Instance 1: Watch for score changes
go func() {
    redisStore.Watch(ctx, func(name string, data *store.ProviderScoreData) {
        log.Printf("Score updated: %s -> effective score: %.2f",
            name, data.BaseScore - data.HealthPenalty)
    })
}()

// Instance 2: Update score (will be published to watchers)
data := &store.ProviderScoreData{
    Name:          "provider1",
    BaseScore:     100.0,
    HealthPenalty: 10.0,
}
redisStore.SetScore(ctx, data) // Automatically publishes to watchers
```

**Custom TTL per Score:**

```go
// Set score with custom expiration
data := &store.ProviderScoreData{
    Name:      "provider1",
    BaseScore: 100.0,
}
redisStore.SetScoreWithTTL(ctx, data, 1*time.Hour)
```

**Multi-Tenancy with Key Prefixes:**

```go
// Tenant 1
config1 := store.RedisConfig{
    Addr:      "localhost:6379",
    KeyPrefix: "tenant1:scoring:",
}
store1, _ := store.NewRedisStore(config1)

// Tenant 2
config2 := store.RedisConfig{
    Addr:      "localhost:6379",
    KeyPrefix: "tenant2:scoring:",
}
store2, _ := store.NewRedisStore(config2)

// Stores are isolated by key prefix
```

### PostgreSQL Store

**Status:** ✅ Available (Phase 3)

Durable database storage with migrations. Suitable for:

- Long-term persistence
- Audit trails and analytics
- Mission-critical deployments
- Historical data retention
- ACID transaction guarantees

**Features:**

- ✅ Automatic schema migrations on startup
- ✅ Batch write operations via transactions
- ✅ Connection pooling (pgxpool)
- ✅ UPSERT support (INSERT ... ON CONFLICT)
- ✅ Custom table prefixing
- ✅ Nullable timestamp handling
- ✅ JSONB for latency arrays

**Characteristics:**

- ✅ Full ACID compliance
- ✅ Durable persistence across restarts
- ✅ Supports historical queries
- ✅ Production-ready connection pooling
- ⚠️ Requires PostgreSQL server
- ⚠️ Slower than Redis/Memory (disk I/O)

**Usage:**

#### Basic PostgreSQL Store

```go
import (
    "github.com/exapsy/chainkit/scoring"
)

// Option 1: Simple configuration
engine := scoring.NewEngine(
    scoring.WithPostgresStore(
        "postgres://user:pass@localhost/chainkit",
    ),
)

// Option 2: With custom settings
engine := scoring.NewEngine(
    scoring.WithPostgresStore(
        "postgres://user:pass@localhost:5432/chainkit?sslmode=require",
        scoring.PostgresTablePrefix("scoring_"),
        scoring.PostgresMaxConns(50),
        scoring.PostgresMaxIdleConns(10),
        scoring.PostgresConnMaxLifetime(10 * time.Minute),
    ),
)

// Option 3: Via StoreConfig
config := store.StoreConfig{
    Type: store.StoreTypePostgres,
    Postgres: &store.PostgresConfig{
        ConnectionString: "postgres://user:pass@localhost/chainkit",
        TablePrefix:      "scoring_",
        MaxOpenConns:     25,
        MaxIdleConns:     5,
        ConnMaxLifetime:  5 * time.Minute,
    },
}

pgStore, err := store.NewStore(config)
if err != nil {
    log.Fatal(err)
}

engine := scoring.NewEngine(scoring.WithStore(pgStore))
```

#### Database Schema

The PostgreSQL store automatically creates tables on first use:

```sql
-- Provider scores table
CREATE TABLE chainkit_provider_scores (
    provider_name       VARCHAR(255) PRIMARY KEY,
    base_score          DOUBLE PRECISION NOT NULL,
    health_penalty      DOUBLE PRECISION NOT NULL DEFAULT 0,
    latency_penalty     DOUBLE PRECISION NOT NULL DEFAULT 0,
    error_penalty       DOUBLE PRECISION NOT NULL DEFAULT 0,
    rate_limit_penalty  DOUBLE PRECISION NOT NULL DEFAULT 0,
    total_operations    BIGINT NOT NULL DEFAULT 0,
    successful_ops      BIGINT NOT NULL DEFAULT 0,
    failed_ops          BIGINT NOT NULL DEFAULT 0,
    last_health_check   TIMESTAMP WITH TIME ZONE,
    last_operation      TIMESTAMP WITH TIME ZONE,
    recent_latencies    JSONB,
    created_at          TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at          TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Latency statistics table
CREATE TABLE chainkit_latency_stats (
    id                  INTEGER PRIMARY KEY DEFAULT 1,
    provider_samples    JSONB NOT NULL,
    updated_at          TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Indexes for performance
CREATE INDEX idx_chainkit_provider_scores_updated
    ON chainkit_provider_scores(updated_at DESC);
CREATE INDEX idx_chainkit_provider_scores_base
    ON chainkit_provider_scores(base_score DESC);
```

#### Advanced Features

**Custom Table Prefix (Multi-Tenancy):**

```go
// Tenant 1
engine1 := scoring.NewEngine(
    scoring.WithPostgresStore(
        "postgres://user:pass@localhost/chainkit",
        scoring.PostgresTablePrefix("tenant1_"),
    ),
)

// Tenant 2
engine2 := scoring.NewEngine(
    scoring.WithPostgresStore(
        "postgres://user:pass@localhost/chainkit",
        scoring.PostgresTablePrefix("tenant2_"),
    ),
)

// Tables are isolated:
// tenant1_provider_scores
// tenant2_provider_scores
```

**Connection Pool Tuning:**

```go
engine := scoring.NewEngine(
    scoring.WithPostgresStore(
        "postgres://user:pass@localhost/chainkit",
        scoring.PostgresMaxConns(100),        // High traffic
        scoring.PostgresMaxIdleConns(20),     // Keep connections warm
        scoring.PostgresConnMaxLifetime(15 * time.Minute),
    ),
)
```

**Long-Term Persistence:**

```go
// Scores are automatically persisted to disk
engine := scoring.NewEngine(
    scoring.WithPostgresStore("postgres://user:pass@localhost/chainkit"),
)

engine.RegisterProvider("provider1", 1)
engine.RecordEvent(scoring.ScoreEvent{...})

// Save to database
ctx := context.Background()
engine.SaveToStore(ctx)

// Restart server...

// Scores automatically loaded on engine creation
engine2 := scoring.NewEngine(
    scoring.WithPostgresStore("postgres://user:pass@localhost/chainkit"),
)
// All scores restored!
```

### Hybrid Store

**Status:** ✅ Available (Phase 4)

Combined cache (Redis) and primary (PostgreSQL) storage. Suitable for:

- High-performance production deployments
- Best of both worlds (speed + durability)
- Multi-instance with persistence

**Features:**

- Write-through and write-behind caching modes
- Synchronous or asynchronous cache writes
- Cache invalidation on write (optional)
- Automatic cache population on miss
- Configurable cache TTL
- Cache warmup support

**Usage:**

```go
// Using scoring engine options (recommended)
engine := scoring.NewEngine(
    scoring.WithHybridStore(
        "postgres://user:pass@localhost/chainkit",  // Primary (PostgreSQL)
        "localhost:6379",                            // Cache (Redis)
        scoring.HybridCacheTTL(5*time.Minute),
        scoring.HybridWriteThrough(true),
    ),
)

// Or using store config directly
config := store.StoreConfig{
    Type: store.StoreTypeHybrid,
    Hybrid: &store.HybridConfig{
        Primary: store.StoreConfig{
            Type: store.StoreTypePostgres,
            Postgres: &store.PostgresConfig{
                ConnectionString: "postgres://user:pass@localhost/chainkit",
            },
        },
        Cache: store.StoreConfig{
            Type: store.StoreTypeRedis,
            Redis: &store.RedisConfig{
                Addr: "localhost:6379",
            },
        },
        CacheTTL:          5 * time.Minute,
        WriteThrough:      true,
        AsyncWrite:        false,
        InvalidateOnWrite: false,
    },
}

hybridStore, err := store.NewStore(config)
if err != nil {
    log.Fatal(err)
}

engine := scoring.NewEngine(scoring.WithStore(hybridStore))
```

**Characteristics:**

| Feature        | Value                                     |
| -------------- | ----------------------------------------- |
| Persistence    | Durable (via PostgreSQL)                  |
| Performance    | Fast reads (via Redis cache)              |
| Multi-instance | Yes (shared state)                        |
| Pub/Sub        | Yes (via Redis)                           |
| TTL            | Yes (cache expiration)                    |
| Transactions   | Yes (via PostgreSQL)                      |
| Cache modes    | Write-through, write-behind, invalidation |
| Latency        | ~400µs (cache hit), ~2ms (cache miss)     |

**Advanced Configuration:**

```go
engine := scoring.NewEngine(
    scoring.WithHybridStore(
        "postgres://user:pass@localhost/chainkit",
        "localhost:6379",
        // Cache settings
        scoring.HybridCacheTTL(10*time.Minute),
        scoring.HybridWriteThrough(true),      // Write to cache on updates
        scoring.HybridAsyncWrite(true),        // Async cache writes
        scoring.HybridInvalidateOnWrite(false), // Don't invalidate, update instead
        // Customize primary store
        scoring.HybridPrimaryConfig(func(cfg *store.StoreConfig) {
            cfg.Postgres.TablePrefix = "myapp_"
            cfg.Postgres.MaxOpenConns = 50
        }),
        // Customize cache store
        scoring.HybridCacheConfig(func(cfg *store.StoreConfig) {
            cfg.Redis.KeyPrefix = "myapp:scoring:"
            cfg.Redis.PoolSize = 20
        }),
    ),
)
```

**Cache Operations:**

```go
hybridStore := engine.GetStore().(*store.HybridStore)

// Warm cache with all scores from primary
err := hybridStore.WarmCache(ctx)

// Invalidate all cache entries (forces refresh from primary)
hybridStore.InvalidateAll()

// Access underlying stores
primaryStore := hybridStore.GetPrimary()
cacheStore := hybridStore.GetCache()
```

## Manual Persistence

### Save to Store

Persist all current scores to the store:

```go
ctx := context.Background()

err := engine.SaveToStore(ctx)
if err != nil {
    log.Printf("Failed to save scores: %v", err)
}
```

### Load from Store

Load all scores from the store (overwrites in-memory state):

```go
ctx := context.Background()

err := engine.LoadFromStore(ctx)
if err != nil {
    log.Printf("Failed to load scores: %v", err)
}
```

### Persist Single Provider

Efficiently persist a single provider's score:

```go
ctx := context.Background()

err := engine.PersistScore(ctx, "provider-name")
if err != nil {
    log.Printf("Failed to persist score: %v", err)
}
```

## Migration Between Stores

### From Memory to Persistent Store

```go
// Old engine with memory store
oldEngine := scoring.NewEngine()
oldEngine.RegisterProvider("provider1", 1)
// ... use engine ...

// Create new store
newStore := store.NewMemoryStore() // or Redis/Postgres in Phase 2/3

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

### Export and Import

```go
// Export all scores
ctx := context.Background()
allScores, err := engine.GetStore().GetAllScores(ctx)
if err != nil {
    log.Fatal(err)
}

// Import to new store
newStore := store.NewMemoryStore()
err = newStore.SetScores(ctx, allScores)
if err != nil {
    log.Fatal(err)
}
```

## Best Practices

### 1. Use Context Timeouts

Always use context with timeouts for store operations:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

err := engine.SaveToStore(ctx)
```

### 2. Handle Store Errors Gracefully

Store operations can fail - handle errors appropriately:

```go
if err := engine.SaveToStore(ctx); err != nil {
    // Log error but don't crash
    log.Printf("Warning: failed to persist scores: %v", err)
    // Continue execution
}
```

### 3. Periodic Persistence

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

### 4. Graceful Shutdown

Persist scores before shutting down:

```go
// Shutdown handler
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

### 5. Check Store Health

Periodically check store connectivity:

```go
ctx := context.Background()
if err := engine.GetStore().Ping(ctx); err != nil {
    log.Printf("Store health check failed: %v", err)
    // Consider circuit breaker or fallback
}
```

## Custom Store Implementation

Implement the `ScoreStore` interface to create your own store:

```go
type MyCustomStore struct {
    // Your fields
}

func (s *MyCustomStore) GetScore(ctx context.Context, name string) (*store.ProviderScoreData, error) {
    // Your implementation
}

// ... implement all other methods ...

func (s *MyCustomStore) Name() string {
    return "custom"
}

// Register factory
func init() {
    store.Register("custom", func(config store.StoreConfig) (store.ScoreStore, error) {
        return &MyCustomStore{}, nil
    })
}
```

## Testing

### Unit Tests

Test your code with the memory store:

```go
func TestMyFunction(t *testing.T) {
    memStore := store.NewMemoryStore()
    engine := scoring.NewEngine(scoring.WithStore(memStore))

    // Test your code
}
```

### Integration Tests

For Redis/Postgres stores (Phase 2+), use testcontainers:

```go
// Phase 2+ - Not yet implemented
func TestWithRedis(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    // Start Redis container
    redisContainer := startRedisContainer(t)
    defer redisContainer.Terminate()

    // Create Redis store
    config := store.StoreConfig{
        Type: store.StoreTypeRedis,
        Redis: &store.RedisConfig{
            Addr: redisContainer.Address(),
        },
    }

    redisStore, err := store.NewStore(config)
    if err != nil {
        t.Fatal(err)
    }

    // Test with real Redis
}
```

## Troubleshooting

### Store is nil

If `engine.GetStore()` returns `nil`, you didn't configure a store. This is normal for default engines:

```go
engine := scoring.NewEngine() // No store
// engine.GetStore() == nil

// To fix, configure a store:
engine = scoring.NewEngine(scoring.WithMemoryStore())
// engine.GetStore() != nil
```

### Data not persisting

Ensure you're calling `SaveToStore`:

```go
// Scores are only in memory until you save
engine.RecordEvent(event)

// Persist to store
ctx := context.Background()
engine.SaveToStore(ctx)
```

### Scores not loading on restart

Ensure you're using the same store instance/configuration:

```go
// Wrong - different store instances
engine1 := scoring.NewEngine(scoring.WithMemoryStore())
engine2 := scoring.NewEngine(scoring.WithMemoryStore()) // New store!

// Right - shared store
sharedStore := store.NewMemoryStore()
engine1 := scoring.NewEngine(scoring.WithStore(sharedStore))
engine2 := scoring.NewEngine(scoring.WithStore(sharedStore))
```

## Performance Considerations

### Memory Store

- **Reads:** O(1) - Hash map lookup
- **Writes:** O(1) - Hash map insert
- **GetAllScores:** O(n) - Iterate all providers
- **Thread-safe:** Uses RWMutex

### Optimization Tips

1. **Batch writes** - Use `SetScores()` instead of multiple `SetScore()` calls
2. **Periodic saves** - Don't save after every event, batch with timers
3. **Context timeouts** - Prevent hanging on store operations
4. **Connection pooling** - For Redis/Postgres (Phase 2+)

## Roadmap

- [x] **Phase 1** - Core interface + Memory store
- [x] **Phase 2** - Redis store (distributed cache)
- [x] **Phase 3** - PostgreSQL store (durable persistence)
- [x] **Phase 4** - Hybrid store (cache + database) ✅
- [ ] **Phase 5** - Metrics and observability
- [ ] **Phase 6** - Advanced features (batching, history)

## Contributing

See [PERSISTENCE_PLAN.md](../PERSISTENCE_PLAN.md) for detailed implementation plans and design decisions.

## License

Part of the chainkit project - see root LICENSE file.
