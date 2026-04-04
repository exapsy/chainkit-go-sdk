# Persistence Plan for Scoring Engine

This document outlines the plan for adding persistence options to the chainkit scoring engine, allowing scores to be stored in:

- **Runtime only** (current behavior, default)
- **Redis** (fast, ephemeral/semi-persistent)
- **Database** (PostgreSQL, fully persistent)
- **Hybrid** (database as source of truth, Redis as cache)

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Storage Interface](#storage-interface)
- [Implementation Details](#implementation-details)
  - [Runtime Storage](#runtime-storage)
  - [Redis Storage](#redis-storage)
  - [Database Storage](#database-storage)
  - [Hybrid Storage](#hybrid-storage)
- [Metrics & Observability](#metrics--observability)
  - [Metrics Interface](#metrics-interface)
  - [Prometheus Instrumentation](#prometheus-instrumentation)
  - [OpenTelemetry Instrumentation](#opentelemetry-instrumentation)
  - [Metrics Configuration](#metrics-configuration)
- [Configuration API](#configuration-api)
- [Data Model](#data-model)
- [Migration & Backwards Compatibility](#migration--backwards-compatibility)
- [Performance Considerations](#performance-considerations)
- [Implementation Phases](#implementation-phases)
- [Testing Strategy](#testing-strategy)

---

## Overview

### Goals

1. **Persistence across restarts**: Scores survive service restarts
2. **Shared state**: Multiple service instances share scoring data
3. **Configurable**: Users choose storage backend based on their needs
4. **Backwards compatible**: Runtime-only mode remains the default
5. **Minimal overhead**: Persistence shouldn't significantly impact performance

### Use Cases

| Storage Mode | Use Case                                               |
| ------------ | ------------------------------------------------------ |
| Runtime      | Single instance, scores reset on restart acceptable    |
| Redis        | Multiple instances, fast access, scores can be rebuilt |
| Database     | Audit trail needed, historical analysis, compliance    |
| Hybrid       | Best of both: fast reads (Redis) + durability (DB)     |

---

## Architecture

### High-Level Design

```
┌─────────────────────────────────────────────────────────────────┐
│                       Scoring Engine                             │
│  ┌─────────────────────────────────────────────────────────────┐ │
│  │                    ScoreStore Interface                      │ │
│  └─────────────────────────────────────────────────────────────┘ │
│         │              │              │              │           │
│         ▼              ▼              ▼              ▼           │
│  ┌───────────┐  ┌───────────┐  ┌───────────┐  ┌───────────────┐ │
│  │  Memory   │  │   Redis   │  │  Database │  │    Hybrid     │ │
│  │  Store    │  │   Store   │  │   Store   │  │ (DB + Redis)  │ │
│  └───────────┘  └───────────┘  └───────────┘  └───────────────┘ │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
```

### Component Interaction

```
┌─────────────┐     RecordEvent()      ┌──────────────┐
│   Provider  │ ─────────────────────► │    Engine    │
│   Manager   │                        │              │
└─────────────┘                        └──────┬───────┘
                                              │
                                              ▼
                                       ┌──────────────┐
                                       │  ScoreStore  │
                                       │  Interface   │
                                       └──────┬───────┘
                                              │
                    ┌─────────────────────────┼─────────────────────────┐
                    │                         │                         │
                    ▼                         ▼                         ▼
             ┌────────────┐           ┌─────────────┐           ┌─────────────┐
             │   Memory   │           │    Redis    │           │  PostgreSQL │
             └────────────┘           └─────────────┘           └─────────────┘
```

---

## Storage Interface

### Core Interface

```go
// scoring/store/store.go

package store

import (
    "context"
    "time"
)

// ProviderScoreData represents the serializable score data
type ProviderScoreData struct {
    Name             string        `json:"name"`
    BaseScore        float64       `json:"base_score"`
    HealthPenalty    float64       `json:"health_penalty"`
    LatencyPenalty   float64       `json:"latency_penalty"`
    ErrorPenalty     float64       `json:"error_penalty"`
    RateLimitPenalty float64       `json:"rate_limit_penalty"`
    TotalOperations  int64         `json:"total_operations"`
    SuccessfulOps    int64         `json:"successful_ops"`
    FailedOps        int64         `json:"failed_ops"`
    LastUpdated      time.Time     `json:"last_updated"`

    // Latency samples (optional, for full state restoration)
    RecentLatencies  []int64       `json:"recent_latencies,omitempty"` // nanoseconds
}

// LatencyStatsData represents global latency statistics
type LatencyStatsData struct {
    ProviderSamples map[string][]int64 `json:"provider_samples"` // nanoseconds
    LastUpdated     time.Time          `json:"last_updated"`
}

// ScoreStore defines the interface for score persistence
type ScoreStore interface {
    // Provider score operations
    GetScore(ctx context.Context, providerName string) (*ProviderScoreData, error)
    SetScore(ctx context.Context, data *ProviderScoreData) error
    GetAllScores(ctx context.Context) ([]*ProviderScoreData, error)
    DeleteScore(ctx context.Context, providerName string) error

    // Batch operations (for efficiency)
    SetScores(ctx context.Context, data []*ProviderScoreData) error

    // Latency data operations
    GetLatencyStats(ctx context.Context) (*LatencyStatsData, error)
    SetLatencyStats(ctx context.Context, data *LatencyStatsData) error

    // Lifecycle
    Close() error
    Ping(ctx context.Context) error

    // Metadata
    Name() string
}

// Optional interfaces for extended functionality

// Watchable allows subscribing to score changes (useful for distributed systems)
type Watchable interface {
    Watch(ctx context.Context, callback func(providerName string, data *ProviderScoreData)) error
}

// Expirable allows setting TTL on stored data
type Expirable interface {
    SetScoreWithTTL(ctx context.Context, data *ProviderScoreData, ttl time.Duration) error
}

// Lockable provides distributed locking for score updates
type Lockable interface {
    Lock(ctx context.Context, providerName string, ttl time.Duration) (unlock func(), err error)
}
```

### Store Registry

```go
// scoring/store/registry.go

package store

import "fmt"

// StoreType represents the type of storage backend
type StoreType string

const (
    StoreTypeMemory   StoreType = "memory"
    StoreTypeRedis    StoreType = "redis"
    StoreTypePostgres StoreType = "postgres"
    StoreTypeHybrid   StoreType = "hybrid"
)

// StoreFactory creates stores based on configuration
type StoreFactory func(config StoreConfig) (ScoreStore, error)

var factories = make(map[StoreType]StoreFactory)

// Register registers a store factory
func Register(storeType StoreType, factory StoreFactory) {
    factories[storeType] = factory
}

// NewStore creates a store based on the configuration
func NewStore(config StoreConfig) (ScoreStore, error) {
    factory, ok := factories[config.Type]
    if !ok {
        return nil, fmt.Errorf("unknown store type: %s", config.Type)
    }
    return factory(config)
}
```

---

## Implementation Details

### Runtime Storage

**File:** `scoring/store/memory.go`

The current in-memory implementation, wrapped to implement `ScoreStore`.

```go
package store

import (
    "context"
    "sync"
    "time"
)

type MemoryStore struct {
    scores   map[string]*ProviderScoreData
    latency  *LatencyStatsData
    mu       sync.RWMutex
}

func NewMemoryStore() *MemoryStore {
    return &MemoryStore{
        scores: make(map[string]*ProviderScoreData),
    }
}

func (m *MemoryStore) Name() string { return "memory" }

func (m *MemoryStore) GetScore(ctx context.Context, name string) (*ProviderScoreData, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()

    if data, ok := m.scores[name]; ok {
        return data, nil
    }
    return nil, nil // Not found, not an error
}

func (m *MemoryStore) SetScore(ctx context.Context, data *ProviderScoreData) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    m.scores[data.Name] = data
    return nil
}

// ... other methods

func (m *MemoryStore) Close() error { return nil }
func (m *MemoryStore) Ping(ctx context.Context) error { return nil }
```

**Characteristics:**

- Zero external dependencies
- Fastest performance
- Data lost on restart
- Single instance only

---

### Redis Storage

**File:** `scoring/store/redis.go`

```go
package store

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "github.com/redis/go-redis/v9"
)

const (
    redisKeyPrefix     = "chainkit:scoring:"
    redisScoreKey      = redisKeyPrefix + "score:"
    redisLatencyKey    = redisKeyPrefix + "latency"
    redisAllScoresKey  = redisKeyPrefix + "providers"
)

type RedisConfig struct {
    Addr         string
    Password     string
    DB           int
    PoolSize     int
    MinIdleConns int

    // TTL for score data (0 = no expiry)
    ScoreTTL     time.Duration

    // Prefix for all keys (for multi-tenant usage)
    KeyPrefix    string
}

type RedisStore struct {
    client    *redis.Client
    config    RedisConfig
    keyPrefix string
}

func NewRedisStore(config RedisConfig) (*RedisStore, error) {
    client := redis.NewClient(&redis.Options{
        Addr:         config.Addr,
        Password:     config.Password,
        DB:           config.DB,
        PoolSize:     config.PoolSize,
        MinIdleConns: config.MinIdleConns,
    })

    // Verify connection
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    if err := client.Ping(ctx).Err(); err != nil {
        return nil, fmt.Errorf("redis connection failed: %w", err)
    }

    keyPrefix := redisKeyPrefix
    if config.KeyPrefix != "" {
        keyPrefix = config.KeyPrefix + ":"
    }

    return &RedisStore{
        client:    client,
        config:    config,
        keyPrefix: keyPrefix,
    }, nil
}

func (r *RedisStore) Name() string { return "redis" }

func (r *RedisStore) scoreKey(name string) string {
    return r.keyPrefix + "score:" + name
}

func (r *RedisStore) GetScore(ctx context.Context, name string) (*ProviderScoreData, error) {
    data, err := r.client.Get(ctx, r.scoreKey(name)).Bytes()
    if err == redis.Nil {
        return nil, nil // Not found
    }
    if err != nil {
        return nil, fmt.Errorf("redis get: %w", err)
    }

    var score ProviderScoreData
    if err := json.Unmarshal(data, &score); err != nil {
        return nil, fmt.Errorf("unmarshal score: %w", err)
    }

    return &score, nil
}

func (r *RedisStore) SetScore(ctx context.Context, data *ProviderScoreData) error {
    bytes, err := json.Marshal(data)
    if err != nil {
        return fmt.Errorf("marshal score: %w", err)
    }

    key := r.scoreKey(data.Name)

    if r.config.ScoreTTL > 0 {
        return r.client.Set(ctx, key, bytes, r.config.ScoreTTL).Err()
    }
    return r.client.Set(ctx, key, bytes, 0).Err()
}

func (r *RedisStore) SetScores(ctx context.Context, data []*ProviderScoreData) error {
    pipe := r.client.Pipeline()

    for _, score := range data {
        bytes, err := json.Marshal(score)
        if err != nil {
            return fmt.Errorf("marshal score %s: %w", score.Name, err)
        }

        key := r.scoreKey(score.Name)
        if r.config.ScoreTTL > 0 {
            pipe.Set(ctx, key, bytes, r.config.ScoreTTL)
        } else {
            pipe.Set(ctx, key, bytes, 0)
        }
    }

    _, err := pipe.Exec(ctx)
    return err
}

func (r *RedisStore) GetAllScores(ctx context.Context) ([]*ProviderScoreData, error) {
    // Use SCAN to find all score keys
    var cursor uint64
    var scores []*ProviderScoreData
    pattern := r.keyPrefix + "score:*"

    for {
        keys, nextCursor, err := r.client.Scan(ctx, cursor, pattern, 100).Result()
        if err != nil {
            return nil, fmt.Errorf("redis scan: %w", err)
        }

        if len(keys) > 0 {
            values, err := r.client.MGet(ctx, keys...).Result()
            if err != nil {
                return nil, fmt.Errorf("redis mget: %w", err)
            }

            for _, v := range values {
                if v == nil {
                    continue
                }
                var score ProviderScoreData
                if err := json.Unmarshal([]byte(v.(string)), &score); err != nil {
                    continue // Skip invalid data
                }
                scores = append(scores, &score)
            }
        }

        cursor = nextCursor
        if cursor == 0 {
            break
        }
    }

    return scores, nil
}

func (r *RedisStore) Close() error {
    return r.client.Close()
}

func (r *RedisStore) Ping(ctx context.Context) error {
    return r.client.Ping(ctx).Err()
}

// Implement Watchable for pub/sub support
func (r *RedisStore) Watch(ctx context.Context, callback func(string, *ProviderScoreData)) error {
    pubsub := r.client.Subscribe(ctx, r.keyPrefix+"events")
    defer pubsub.Close()

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case msg := <-pubsub.Channel():
            var data ProviderScoreData
            if err := json.Unmarshal([]byte(msg.Payload), &data); err == nil {
                callback(data.Name, &data)
            }
        }
    }
}

// Implement Lockable for distributed locking
func (r *RedisStore) Lock(ctx context.Context, name string, ttl time.Duration) (func(), error) {
    lockKey := r.keyPrefix + "lock:" + name
    lockValue := fmt.Sprintf("%d", time.Now().UnixNano())

    ok, err := r.client.SetNX(ctx, lockKey, lockValue, ttl).Result()
    if err != nil {
        return nil, err
    }
    if !ok {
        return nil, fmt.Errorf("lock already held")
    }

    return func() {
        // Only delete if we still own the lock
        script := `
            if redis.call("get", KEYS[1]) == ARGV[1] then
                return redis.call("del", KEYS[1])
            else
                return 0
            end
        `
        r.client.Eval(ctx, script, []string{lockKey}, lockValue)
    }, nil
}
```

**Characteristics:**

- Fast reads and writes
- Shared across instances
- Optional TTL for auto-cleanup
- Pub/sub for distributed updates
- Data survives restarts (if Redis persists)

---

### Database Storage

**File:** `scoring/store/postgres.go`

```go
package store

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "time"

    _ "github.com/lib/pq"
)

type PostgresConfig struct {
    ConnectionString string
    TablePrefix      string // Default: "chainkit_"
    MaxOpenConns     int
    MaxIdleConns     int
    ConnMaxLifetime  time.Duration
}

type PostgresStore struct {
    db          *sql.DB
    tablePrefix string
}

func NewPostgresStore(config PostgresConfig) (*PostgresStore, error) {
    db, err := sql.Open("postgres", config.ConnectionString)
    if err != nil {
        return nil, fmt.Errorf("open postgres: %w", err)
    }

    if config.MaxOpenConns > 0 {
        db.SetMaxOpenConns(config.MaxOpenConns)
    }
    if config.MaxIdleConns > 0 {
        db.SetMaxIdleConns(config.MaxIdleConns)
    }
    if config.ConnMaxLifetime > 0 {
        db.SetConnMaxLifetime(config.ConnMaxLifetime)
    }

    // Verify connection
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    if err := db.PingContext(ctx); err != nil {
        return nil, fmt.Errorf("postgres ping: %w", err)
    }

    tablePrefix := "chainkit_"
    if config.TablePrefix != "" {
        tablePrefix = config.TablePrefix
    }

    store := &PostgresStore{
        db:          db,
        tablePrefix: tablePrefix,
    }

    // Create tables if not exist
    if err := store.migrate(ctx); err != nil {
        return nil, fmt.Errorf("migration: %w", err)
    }

    return store, nil
}

func (p *PostgresStore) migrate(ctx context.Context) error {
    schema := fmt.Sprintf(`
        CREATE TABLE IF NOT EXISTS %sprovider_scores (
            provider_name     VARCHAR(255) PRIMARY KEY,
            base_score        DOUBLE PRECISION NOT NULL,
            health_penalty    DOUBLE PRECISION NOT NULL DEFAULT 0,
            latency_penalty   DOUBLE PRECISION NOT NULL DEFAULT 0,
            error_penalty     DOUBLE PRECISION NOT NULL DEFAULT 0,
            rate_limit_penalty DOUBLE PRECISION NOT NULL DEFAULT 0,
            total_operations  BIGINT NOT NULL DEFAULT 0,
            successful_ops    BIGINT NOT NULL DEFAULT 0,
            failed_ops        BIGINT NOT NULL DEFAULT 0,
            recent_latencies  JSONB,
            created_at        TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
            updated_at        TIMESTAMP WITH TIME ZONE DEFAULT NOW()
        );

        CREATE TABLE IF NOT EXISTS %slatency_stats (
            id                SERIAL PRIMARY KEY,
            provider_samples  JSONB NOT NULL,
            updated_at        TIMESTAMP WITH TIME ZONE DEFAULT NOW()
        );

        CREATE INDEX IF NOT EXISTS idx_%sprovider_scores_updated
            ON %sprovider_scores(updated_at);
    `, p.tablePrefix, p.tablePrefix, p.tablePrefix, p.tablePrefix)

    _, err := p.db.ExecContext(ctx, schema)
    return err
}

func (p *PostgresStore) Name() string { return "postgres" }

func (p *PostgresStore) scoresTable() string {
    return p.tablePrefix + "provider_scores"
}

func (p *PostgresStore) latencyTable() string {
    return p.tablePrefix + "latency_stats"
}

func (p *PostgresStore) GetScore(ctx context.Context, name string) (*ProviderScoreData, error) {
    query := fmt.Sprintf(`
        SELECT provider_name, base_score, health_penalty, latency_penalty,
               error_penalty, rate_limit_penalty, total_operations,
               successful_ops, failed_ops, recent_latencies, updated_at
        FROM %s
        WHERE provider_name = $1
    `, p.scoresTable())

    var data ProviderScoreData
    var latenciesJSON []byte

    err := p.db.QueryRowContext(ctx, query, name).Scan(
        &data.Name,
        &data.BaseScore,
        &data.HealthPenalty,
        &data.LatencyPenalty,
        &data.ErrorPenalty,
        &data.RateLimitPenalty,
        &data.TotalOperations,
        &data.SuccessfulOps,
        &data.FailedOps,
        &latenciesJSON,
        &data.LastUpdated,
    )

    if err == sql.ErrNoRows {
        return nil, nil
    }
    if err != nil {
        return nil, fmt.Errorf("query score: %w", err)
    }

    if latenciesJSON != nil {
        json.Unmarshal(latenciesJSON, &data.RecentLatencies)
    }

    return &data, nil
}

func (p *PostgresStore) SetScore(ctx context.Context, data *ProviderScoreData) error {
    latenciesJSON, _ := json.Marshal(data.RecentLatencies)

    query := fmt.Sprintf(`
        INSERT INTO %s (
            provider_name, base_score, health_penalty, latency_penalty,
            error_penalty, rate_limit_penalty, total_operations,
            successful_ops, failed_ops, recent_latencies, updated_at
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
        ON CONFLICT (provider_name) DO UPDATE SET
            base_score = EXCLUDED.base_score,
            health_penalty = EXCLUDED.health_penalty,
            latency_penalty = EXCLUDED.latency_penalty,
            error_penalty = EXCLUDED.error_penalty,
            rate_limit_penalty = EXCLUDED.rate_limit_penalty,
            total_operations = EXCLUDED.total_operations,
            successful_ops = EXCLUDED.successful_ops,
            failed_ops = EXCLUDED.failed_ops,
            recent_latencies = EXCLUDED.recent_latencies,
            updated_at = NOW()
    `, p.scoresTable())

    _, err := p.db.ExecContext(ctx, query,
        data.Name,
        data.BaseScore,
        data.HealthPenalty,
        data.LatencyPenalty,
        data.ErrorPenalty,
        data.RateLimitPenalty,
        data.TotalOperations,
        data.SuccessfulOps,
        data.FailedOps,
        latenciesJSON,
    )

    return err
}

func (p *PostgresStore) SetScores(ctx context.Context, data []*ProviderScoreData) error {
    tx, err := p.db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()

    for _, score := range data {
        if err := p.setScoreTx(ctx, tx, score); err != nil {
            return err
        }
    }

    return tx.Commit()
}

func (p *PostgresStore) setScoreTx(ctx context.Context, tx *sql.Tx, data *ProviderScoreData) error {
    latenciesJSON, _ := json.Marshal(data.RecentLatencies)

    query := fmt.Sprintf(`
        INSERT INTO %s (
            provider_name, base_score, health_penalty, latency_penalty,
            error_penalty, rate_limit_penalty, total_operations,
            successful_ops, failed_ops, recent_latencies, updated_at
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
        ON CONFLICT (provider_name) DO UPDATE SET
            base_score = EXCLUDED.base_score,
            health_penalty = EXCLUDED.health_penalty,
            latency_penalty = EXCLUDED.latency_penalty,
            error_penalty = EXCLUDED.error_penalty,
            rate_limit_penalty = EXCLUDED.rate_limit_penalty,
            total_operations = EXCLUDED.total_operations,
            successful_ops = EXCLUDED.successful_ops,
            failed_ops = EXCLUDED.failed_ops,
            recent_latencies = EXCLUDED.recent_latencies,
            updated_at = NOW()
    `, p.scoresTable())

    _, err := tx.ExecContext(ctx, query,
        data.Name,
        data.BaseScore,
        data.HealthPenalty,
        data.LatencyPenalty,
        data.ErrorPenalty,
        data.RateLimitPenalty,
        data.TotalOperations,
        data.SuccessfulOps,
        data.FailedOps,
        latenciesJSON,
    )

    return err
}

func (p *PostgresStore) GetAllScores(ctx context.Context) ([]*ProviderScoreData, error) {
    query := fmt.Sprintf(`
        SELECT provider_name, base_score, health_penalty, latency_penalty,
               error_penalty, rate_limit_penalty, total_operations,
               successful_ops, failed_ops, recent_latencies, updated_at
        FROM %s
    `, p.scoresTable())

    rows, err := p.db.QueryContext(ctx, query)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var scores []*ProviderScoreData
    for rows.Next() {
        var data ProviderScoreData
        var latenciesJSON []byte

        err := rows.Scan(
            &data.Name,
            &data.BaseScore,
            &data.HealthPenalty,
            &data.LatencyPenalty,
            &data.ErrorPenalty,
            &data.RateLimitPenalty,
            &data.TotalOperations,
            &data.SuccessfulOps,
            &data.FailedOps,
            &latenciesJSON,
            &data.LastUpdated,
        )
        if err != nil {
            return nil, err
        }

        if latenciesJSON != nil {
            json.Unmarshal(latenciesJSON, &data.RecentLatencies)
        }

        scores = append(scores, &data)
    }

    return scores, rows.Err()
}

func (p *PostgresStore) DeleteScore(ctx context.Context, name string) error {
    query := fmt.Sprintf(`DELETE FROM %s WHERE provider_name = $1`, p.scoresTable())
    _, err := p.db.ExecContext(ctx, query, name)
    return err
}

func (p *PostgresStore) Close() error {
    return p.db.Close()
}

func (p *PostgresStore) Ping(ctx context.Context) error {
    return p.db.PingContext(ctx)
}
```

**Characteristics:**

- Full persistence across restarts
- Historical data retention
- ACID transactions
- Audit trail capability
- Slightly higher latency than Redis

---

### Hybrid Storage

**File:** `scoring/store/hybrid.go`

```go
package store

import (
    "context"
    "sync"
    "time"
)

type HybridConfig struct {
    // Primary store (source of truth)
    Primary ScoreStore

    // Cache store (fast reads)
    Cache ScoreStore

    // Cache TTL (how long to trust cache before checking primary)
    CacheTTL time.Duration

    // WriteThrough: if true, writes go to both; if false, writes go to primary only
    WriteThrough bool

    // AsyncWrite: if true, cache writes happen asynchronously
    AsyncWrite bool
}

type HybridStore struct {
    primary ScoreStore
    cache   ScoreStore
    config  HybridConfig

    // Track cache freshness
    cacheTime map[string]time.Time
    cacheMu   sync.RWMutex
}

func NewHybridStore(config HybridConfig) (*HybridStore, error) {
    if config.CacheTTL == 0 {
        config.CacheTTL = 5 * time.Minute
    }

    return &HybridStore{
        primary:   config.Primary,
        cache:     config.Cache,
        config:    config,
        cacheTime: make(map[string]time.Time),
    }, nil
}

func (h *HybridStore) Name() string {
    return "hybrid(" + h.primary.Name() + "+" + h.cache.Name() + ")"
}

func (h *HybridStore) isCacheFresh(name string) bool {
    h.cacheMu.RLock()
    defer h.cacheMu.RUnlock()

    if t, ok := h.cacheTime[name]; ok {
        return time.Since(t) < h.config.CacheTTL
    }
    return false
}

func (h *HybridStore) markCacheFresh(name string) {
    h.cacheMu.Lock()
    defer h.cacheMu.Unlock()
    h.cacheTime[name] = time.Now()
}

func (h *HybridStore) GetScore(ctx context.Context, name string) (*ProviderScoreData, error) {
    // Try cache first if fresh
    if h.isCacheFresh(name) {
        data, err := h.cache.GetScore(ctx, name)
        if err == nil && data != nil {
            return data, nil
        }
    }

    // Fall back to primary
    data, err := h.primary.GetScore(ctx, name)
    if err != nil {
        return nil, err
    }

    // Populate cache
    if data != nil {
        if h.config.AsyncWrite {
            go h.cache.SetScore(context.Background(), data)
        } else {
            h.cache.SetScore(ctx, data)
        }
        h.markCacheFresh(name)
    }

    return data, nil
}

func (h *HybridStore) SetScore(ctx context.Context, data *ProviderScoreData) error {
    // Always write to primary
    if err := h.primary.SetScore(ctx, data); err != nil {
        return err
    }

    // Update cache
    if h.config.WriteThrough {
        if h.config.AsyncWrite {
            go h.cache.SetScore(context.Background(), data)
        } else {
            h.cache.SetScore(ctx, data)
        }
    }

    h.markCacheFresh(data.Name)
    return nil
}

func (h *HybridStore) SetScores(ctx context.Context, data []*ProviderScoreData) error {
    // Always write to primary
    if err := h.primary.SetScores(ctx, data); err != nil {
        return err
    }

    // Update cache
    if h.config.WriteThrough {
        if h.config.AsyncWrite {
            go h.cache.SetScores(context.Background(), data)
        } else {
            h.cache.SetScores(ctx, data)
        }
    }

    for _, d := range data {
        h.markCacheFresh(d.Name)
    }

    return nil
}

func (h *HybridStore) GetAllScores(ctx context.Context) ([]*ProviderScoreData, error) {
    // Always get from primary for consistency
    return h.primary.GetAllScores(ctx)
}

func (h *HybridStore) DeleteScore(ctx context.Context, name string) error {
    if err := h.primary.DeleteScore(ctx, name); err != nil {
        return err
    }

    h.cache.DeleteScore(ctx, name)

    h.cacheMu.Lock()
    delete(h.cacheTime, name)
    h.cacheMu.Unlock()

    return nil
}

func (h *HybridStore) GetLatencyStats(ctx context.Context) (*LatencyStatsData, error) {
    return h.primary.GetLatencyStats(ctx)
}

func (h *HybridStore) SetLatencyStats(ctx context.Context, data *LatencyStatsData) error {
    return h.primary.SetLatencyStats(ctx, data)
}

func (h *HybridStore) Close() error {
    h.cache.Close()
    return h.primary.Close()
}

func (h *HybridStore) Ping(ctx context.Context) error {
    if err := h.primary.Ping(ctx); err != nil {
        return err
    }
    return h.cache.Ping(ctx)
}
```

**Characteristics:**

- Best of both worlds: fast reads (cache) + durability (database)
- Configurable write-through or write-behind
- Automatic cache population on miss
- TTL-based cache invalidation

---

## Metrics & Observability

The scoring engine provides instrumentation hooks for observability tools. Following OSS best practices, we use **interfaces** rather than direct dependencies, allowing users to plug in their preferred metrics backend (Prometheus, OpenTelemetry, Datadog, etc.) or use the default no-op implementation.

### Design Principles

1. **Zero dependencies by default**: No metrics libraries required
2. **Pluggable backends**: Users bring their own instrumentation
3. **Minimal overhead**: No-op when metrics disabled
4. **Standard interfaces**: Compatible with common observability patterns

### Metrics Interface

```go
// scoring/metrics/metrics.go

package metrics

import (
    "context"
    "time"
)

// Recorder is the core interface for recording scoring metrics.
// Implementations should be thread-safe.
type Recorder interface {
    // Score metrics
    RecordScoreChange(ctx context.Context, provider string, scoreType ScoreType, oldValue, newValue float64)
    RecordEffectiveScore(ctx context.Context, provider string, score float64)

    // Event metrics
    RecordEvent(ctx context.Context, provider string, eventType string, success bool)

    // Latency metrics
    RecordLatency(ctx context.Context, provider string, operation string, duration time.Duration)

    // Store metrics
    RecordStoreOperation(ctx context.Context, store string, operation string, duration time.Duration, err error)
    RecordCacheHit(ctx context.Context, store string, hit bool)

    // Provider ranking
    RecordProviderRank(ctx context.Context, provider string, rank int, totalProviders int)
}

// ScoreType represents the type of score component
type ScoreType string

const (
    ScoreTypeBase       ScoreType = "base"
    ScoreTypeHealth     ScoreType = "health_penalty"
    ScoreTypeLatency    ScoreType = "latency_penalty"
    ScoreTypeError      ScoreType = "error_penalty"
    ScoreTypeRateLimit  ScoreType = "rate_limit_penalty"
    ScoreTypeEffective  ScoreType = "effective"
)

// Labels provides standard label names for consistency across implementations
var Labels = struct {
    Provider   string
    Operation  string
    EventType  string
    ScoreType  string
    Store      string
    Success    string
    CacheHit   string
}{
    Provider:   "provider",
    Operation:  "operation",
    EventType:  "event_type",
    ScoreType:  "score_type",
    Store:      "store",
    Success:    "success",
    CacheHit:   "cache_hit",
}

// NoOpRecorder is the default recorder that does nothing.
// Used when metrics are not configured.
type NoOpRecorder struct{}

func (n *NoOpRecorder) RecordScoreChange(ctx context.Context, provider string, scoreType ScoreType, oldValue, newValue float64) {}
func (n *NoOpRecorder) RecordEffectiveScore(ctx context.Context, provider string, score float64) {}
func (n *NoOpRecorder) RecordEvent(ctx context.Context, provider string, eventType string, success bool) {}
func (n *NoOpRecorder) RecordLatency(ctx context.Context, provider string, operation string, duration time.Duration) {}
func (n *NoOpRecorder) RecordStoreOperation(ctx context.Context, store string, operation string, duration time.Duration, err error) {}
func (n *NoOpRecorder) RecordCacheHit(ctx context.Context, store string, hit bool) {}
func (n *NoOpRecorder) RecordProviderRank(ctx context.Context, provider string, rank int, totalProviders int) {}

// Ensure NoOpRecorder implements Recorder
var _ Recorder = (*NoOpRecorder)(nil)
```

### Metrics Definitions

| Metric Name                               | Type      | Labels                        | Description                      |
| ----------------------------------------- | --------- | ----------------------------- | -------------------------------- |
| `chainkit_scoring_score`                  | Gauge     | provider, score_type          | Current score value by type      |
| `chainkit_scoring_effective_score`        | Gauge     | provider                      | Current effective score          |
| `chainkit_scoring_events_total`           | Counter   | provider, event_type, success | Total scoring events             |
| `chainkit_scoring_latency_seconds`        | Histogram | provider, operation           | Operation latency                |
| `chainkit_scoring_provider_rank`          | Gauge     | provider                      | Current provider rank (1 = best) |
| `chainkit_scoring_store_operations_total` | Counter   | store, operation, success     | Store operations                 |
| `chainkit_scoring_store_latency_seconds`  | Histogram | store, operation              | Store operation latency          |
| `chainkit_scoring_cache_hits_total`       | Counter   | store, hit                    | Cache hit/miss counts            |
| `chainkit_scoring_providers_total`        | Gauge     |                               | Total registered providers       |

### Prometheus Instrumentation

```go
// scoring/metrics/prometheus/prometheus.go

package prometheus

import (
    "context"
    "time"

    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"

    "github.com/exapsy/chainkit/scoring/metrics"
)

// Config holds Prometheus metrics configuration
type Config struct {
    // Namespace for all metrics (default: "chainkit")
    Namespace string

    // Subsystem for all metrics (default: "scoring")
    Subsystem string

    // Registry to register metrics with (default: prometheus.DefaultRegisterer)
    Registry prometheus.Registerer

    // LatencyBuckets for histogram metrics
    LatencyBuckets []float64
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
    return Config{
        Namespace:      "chainkit",
        Subsystem:      "scoring",
        Registry:       prometheus.DefaultRegisterer,
        LatencyBuckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
    }
}

// Recorder implements metrics.Recorder using Prometheus
type Recorder struct {
    config Config

    // Score metrics
    scoreGauge         *prometheus.GaugeVec
    effectiveScoreGauge *prometheus.GaugeVec

    // Event metrics
    eventsCounter      *prometheus.CounterVec

    // Latency metrics
    latencyHistogram   *prometheus.HistogramVec

    // Provider ranking
    providerRankGauge  *prometheus.GaugeVec
    providersGauge     prometheus.Gauge

    // Store metrics
    storeOpsCounter    *prometheus.CounterVec
    storeLatencyHist   *prometheus.HistogramVec
    cacheHitsCounter   *prometheus.CounterVec
}

// NewRecorder creates a new Prometheus metrics recorder
func NewRecorder(config Config) *Recorder {
    if config.Namespace == "" {
        config.Namespace = "chainkit"
    }
    if config.Subsystem == "" {
        config.Subsystem = "scoring"
    }
    if config.Registry == nil {
        config.Registry = prometheus.DefaultRegisterer
    }
    if len(config.LatencyBuckets) == 0 {
        config.LatencyBuckets = DefaultConfig().LatencyBuckets
    }

    factory := promauto.With(config.Registry)

    return &Recorder{
        config: config,

        scoreGauge: factory.NewGaugeVec(prometheus.GaugeOpts{
            Namespace: config.Namespace,
            Subsystem: config.Subsystem,
            Name:      "score",
            Help:      "Current score value by type",
        }, []string{"provider", "score_type"}),

        effectiveScoreGauge: factory.NewGaugeVec(prometheus.GaugeOpts{
            Namespace: config.Namespace,
            Subsystem: config.Subsystem,
            Name:      "effective_score",
            Help:      "Current effective score per provider",
        }, []string{"provider"}),

        eventsCounter: factory.NewCounterVec(prometheus.CounterOpts{
            Namespace: config.Namespace,
            Subsystem: config.Subsystem,
            Name:      "events_total",
            Help:      "Total scoring events by type and outcome",
        }, []string{"provider", "event_type", "success"}),

        latencyHistogram: factory.NewHistogramVec(prometheus.HistogramOpts{
            Namespace: config.Namespace,
            Subsystem: config.Subsystem,
            Name:      "latency_seconds",
            Help:      "Operation latency in seconds",
            Buckets:   config.LatencyBuckets,
        }, []string{"provider", "operation"}),

        providerRankGauge: factory.NewGaugeVec(prometheus.GaugeOpts{
            Namespace: config.Namespace,
            Subsystem: config.Subsystem,
            Name:      "provider_rank",
            Help:      "Current provider rank (1 = best)",
        }, []string{"provider"}),

        providersGauge: factory.NewGauge(prometheus.GaugeOpts{
            Namespace: config.Namespace,
            Subsystem: config.Subsystem,
            Name:      "providers_total",
            Help:      "Total number of registered providers",
        }),

        storeOpsCounter: factory.NewCounterVec(prometheus.CounterOpts{
            Namespace: config.Namespace,
            Subsystem: config.Subsystem,
            Name:      "store_operations_total",
            Help:      "Total store operations by type and outcome",
        }, []string{"store", "operation", "success"}),

        storeLatencyHist: factory.NewHistogramVec(prometheus.HistogramOpts{
            Namespace: config.Namespace,
            Subsystem: config.Subsystem,
            Name:      "store_latency_seconds",
            Help:      "Store operation latency in seconds",
            Buckets:   config.LatencyBuckets,
        }, []string{"store", "operation"}),

        cacheHitsCounter: factory.NewCounterVec(prometheus.CounterOpts{
            Namespace: config.Namespace,
            Subsystem: config.Subsystem,
            Name:      "cache_hits_total",
            Help:      "Cache hit/miss counts",
        }, []string{"store", "hit"}),
    }
}

func (r *Recorder) RecordScoreChange(ctx context.Context, provider string, scoreType metrics.ScoreType, oldValue, newValue float64) {
    r.scoreGauge.WithLabelValues(provider, string(scoreType)).Set(newValue)
}

func (r *Recorder) RecordEffectiveScore(ctx context.Context, provider string, score float64) {
    r.effectiveScoreGauge.WithLabelValues(provider).Set(score)
}

func (r *Recorder) RecordEvent(ctx context.Context, provider string, eventType string, success bool) {
    successStr := "false"
    if success {
        successStr = "true"
    }
    r.eventsCounter.WithLabelValues(provider, eventType, successStr).Inc()
}

func (r *Recorder) RecordLatency(ctx context.Context, provider string, operation string, duration time.Duration) {
    r.latencyHistogram.WithLabelValues(provider, operation).Observe(duration.Seconds())
}

func (r *Recorder) RecordStoreOperation(ctx context.Context, store string, operation string, duration time.Duration, err error) {
    successStr := "true"
    if err != nil {
        successStr = "false"
    }
    r.storeOpsCounter.WithLabelValues(store, operation, successStr).Inc()
    r.storeLatencyHist.WithLabelValues(store, operation).Observe(duration.Seconds())
}

func (r *Recorder) RecordCacheHit(ctx context.Context, store string, hit bool) {
    hitStr := "false"
    if hit {
        hitStr = "true"
    }
    r.cacheHitsCounter.WithLabelValues(store, hitStr).Inc()
}

func (r *Recorder) RecordProviderRank(ctx context.Context, provider string, rank int, totalProviders int) {
    r.providerRankGauge.WithLabelValues(provider).Set(float64(rank))
    r.providersGauge.Set(float64(totalProviders))
}

// Ensure Recorder implements metrics.Recorder
var _ metrics.Recorder = (*Recorder)(nil)
```

### OpenTelemetry Instrumentation

```go
// scoring/metrics/otel/otel.go

package otel

import (
    "context"
    "time"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/metric"

    scoringmetrics "github.com/exapsy/chainkit/scoring/metrics"
)

// Config holds OpenTelemetry metrics configuration
type Config struct {
    // MeterName for the meter (default: "chainkit.scoring")
    MeterName string

    // MeterProvider to use (default: otel.GetMeterProvider())
    MeterProvider metric.MeterProvider
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
    return Config{
        MeterName:     "chainkit.scoring",
        MeterProvider: nil, // Will use global provider
    }
}

// Recorder implements metrics.Recorder using OpenTelemetry
type Recorder struct {
    meter metric.Meter

    // Instruments
    scoreGauge          metric.Float64ObservableGauge
    effectiveScoreGauge metric.Float64ObservableGauge
    eventsCounter       metric.Int64Counter
    latencyHistogram    metric.Float64Histogram
    providerRankGauge   metric.Int64ObservableGauge
    storeOpsCounter     metric.Int64Counter
    storeLatencyHist    metric.Float64Histogram
    cacheHitsCounter    metric.Int64Counter

    // State for observable gauges
    scores          map[string]map[scoringmetrics.ScoreType]float64
    effectiveScores map[string]float64
    providerRanks   map[string]int
    totalProviders  int
}

// NewRecorder creates a new OpenTelemetry metrics recorder
func NewRecorder(config Config) (*Recorder, error) {
    if config.MeterName == "" {
        config.MeterName = "chainkit.scoring"
    }

    provider := config.MeterProvider
    if provider == nil {
        provider = otel.GetMeterProvider()
    }

    meter := provider.Meter(config.MeterName)

    r := &Recorder{
        meter:           meter,
        scores:          make(map[string]map[scoringmetrics.ScoreType]float64),
        effectiveScores: make(map[string]float64),
        providerRanks:   make(map[string]int),
    }

    var err error

    // Observable gauges for scores
    r.scoreGauge, err = meter.Float64ObservableGauge(
        "chainkit.scoring.score",
        metric.WithDescription("Current score value by type"),
        metric.WithFloat64Callback(r.observeScores),
    )
    if err != nil {
        return nil, err
    }

    r.effectiveScoreGauge, err = meter.Float64ObservableGauge(
        "chainkit.scoring.effective_score",
        metric.WithDescription("Current effective score per provider"),
        metric.WithFloat64Callback(r.observeEffectiveScores),
    )
    if err != nil {
        return nil, err
    }

    // Counter for events
    r.eventsCounter, err = meter.Int64Counter(
        "chainkit.scoring.events",
        metric.WithDescription("Total scoring events by type and outcome"),
    )
    if err != nil {
        return nil, err
    }

    // Histogram for latency
    r.latencyHistogram, err = meter.Float64Histogram(
        "chainkit.scoring.latency",
        metric.WithDescription("Operation latency in seconds"),
        metric.WithUnit("s"),
    )
    if err != nil {
        return nil, err
    }

    // Observable gauge for provider ranks
    r.providerRankGauge, err = meter.Int64ObservableGauge(
        "chainkit.scoring.provider_rank",
        metric.WithDescription("Current provider rank (1 = best)"),
        metric.WithInt64Callback(r.observeProviderRanks),
    )
    if err != nil {
        return nil, err
    }

    // Store metrics
    r.storeOpsCounter, err = meter.Int64Counter(
        "chainkit.scoring.store_operations",
        metric.WithDescription("Total store operations by type and outcome"),
    )
    if err != nil {
        return nil, err
    }

    r.storeLatencyHist, err = meter.Float64Histogram(
        "chainkit.scoring.store_latency",
        metric.WithDescription("Store operation latency in seconds"),
        metric.WithUnit("s"),
    )
    if err != nil {
        return nil, err
    }

    r.cacheHitsCounter, err = meter.Int64Counter(
        "chainkit.scoring.cache_hits",
        metric.WithDescription("Cache hit/miss counts"),
    )
    if err != nil {
        return nil, err
    }

    return r, nil
}

func (r *Recorder) observeScores(ctx context.Context, observer metric.Float64Observer) error {
    for provider, scores := range r.scores {
        for scoreType, value := range scores {
            observer.Observe(value,
                metric.WithAttributes(
                    attribute.String("provider", provider),
                    attribute.String("score_type", string(scoreType)),
                ),
            )
        }
    }
    return nil
}

func (r *Recorder) observeEffectiveScores(ctx context.Context, observer metric.Float64Observer) error {
    for provider, score := range r.effectiveScores {
        observer.Observe(score,
            metric.WithAttributes(attribute.String("provider", provider)),
        )
    }
    return nil
}

func (r *Recorder) observeProviderRanks(ctx context.Context, observer metric.Int64Observer) error {
    for provider, rank := range r.providerRanks {
        observer.Observe(int64(rank),
            metric.WithAttributes(attribute.String("provider", provider)),
        )
    }
    return nil
}

func (r *Recorder) RecordScoreChange(ctx context.Context, provider string, scoreType scoringmetrics.ScoreType, oldValue, newValue float64) {
    if r.scores[provider] == nil {
        r.scores[provider] = make(map[scoringmetrics.ScoreType]float64)
    }
    r.scores[provider][scoreType] = newValue
}

func (r *Recorder) RecordEffectiveScore(ctx context.Context, provider string, score float64) {
    r.effectiveScores[provider] = score
}

func (r *Recorder) RecordEvent(ctx context.Context, provider string, eventType string, success bool) {
    r.eventsCounter.Add(ctx, 1,
        metric.WithAttributes(
            attribute.String("provider", provider),
            attribute.String("event_type", eventType),
            attribute.Bool("success", success),
        ),
    )
}

func (r *Recorder) RecordLatency(ctx context.Context, provider string, operation string, duration time.Duration) {
    r.latencyHistogram.Record(ctx, duration.Seconds(),
        metric.WithAttributes(
            attribute.String("provider", provider),
            attribute.String("operation", operation),
        ),
    )
}

func (r *Recorder) RecordStoreOperation(ctx context.Context, store string, operation string, duration time.Duration, err error) {
    r.storeOpsCounter.Add(ctx, 1,
        metric.WithAttributes(
            attribute.String("store", store),
            attribute.String("operation", operation),
            attribute.Bool("success", err == nil),
        ),
    )
    r.storeLatencyHist.Record(ctx, duration.Seconds(),
        metric.WithAttributes(
            attribute.String("store", store),
            attribute.String("operation", operation),
        ),
    )
}

func (r *Recorder) RecordCacheHit(ctx context.Context, store string, hit bool) {
    r.cacheHitsCounter.Add(ctx, 1,
        metric.WithAttributes(
            attribute.String("store", store),
            attribute.Bool("hit", hit),
        ),
    )
}

func (r *Recorder) RecordProviderRank(ctx context.Context, provider string, rank int, totalProviders int) {
    r.providerRanks[provider] = rank
    r.totalProviders = totalProviders
}

// Ensure Recorder implements metrics.Recorder
var _ scoringmetrics.Recorder = (*Recorder)(nil)
```

### Metrics Configuration

```go
// Updated scoring/options.go

// WithMetrics sets the metrics recorder for the scoring engine
func WithMetrics(recorder metrics.Recorder) ScoringOption {
    return func(c *ScoringConfig) {
        c.MetricsRecorder = recorder
    }
}

// WithPrometheusMetrics configures Prometheus metrics with default settings
func WithPrometheusMetrics() ScoringOption {
    return func(c *ScoringConfig) {
        c.MetricsRecorder = prometheus.NewRecorder(prometheus.DefaultConfig())
    }
}

// WithPrometheusMetricsConfig configures Prometheus metrics with custom settings
func WithPrometheusMetricsConfig(config prometheus.Config) ScoringOption {
    return func(c *ScoringConfig) {
        c.MetricsRecorder = prometheus.NewRecorder(config)
    }
}

// WithOTelMetrics configures OpenTelemetry metrics with default settings
func WithOTelMetrics() ScoringOption {
    return func(c *ScoringConfig) {
        recorder, err := otel.NewRecorder(otel.DefaultConfig())
        if err != nil {
            // Fall back to no-op
            c.MetricsRecorder = &metrics.NoOpRecorder{}
            return
        }
        c.MetricsRecorder = recorder
    }
}

// WithOTelMetricsConfig configures OpenTelemetry metrics with custom settings
func WithOTelMetricsConfig(config otel.Config) ScoringOption {
    return func(c *ScoringConfig) {
        recorder, err := otel.NewRecorder(config)
        if err != nil {
            c.MetricsRecorder = &metrics.NoOpRecorder{}
            return
        }
        c.MetricsRecorder = recorder
    }
}
```

### Usage Examples

```go
// No metrics (default - zero overhead)
engine := scoring.NewEngine()

// Prometheus metrics with defaults
engine := scoring.NewEngine(
    scoring.WithPrometheusMetrics(),
)

// Prometheus with custom config
engine := scoring.NewEngine(
    scoring.WithPrometheusMetricsConfig(prometheus.Config{
        Namespace: "myapp",
        Subsystem: "blockchain",
        Registry:  myCustomRegistry,
    }),
)

// OpenTelemetry metrics with defaults
engine := scoring.NewEngine(
    scoring.WithOTelMetrics(),
)

// OpenTelemetry with custom provider
engine := scoring.NewEngine(
    scoring.WithOTelMetricsConfig(otel.Config{
        MeterName:     "myapp.blockchain.scoring",
        MeterProvider: myOTelProvider,
    }),
)

// Custom metrics implementation
type MyRecorder struct { /* ... */ }
func (m *MyRecorder) RecordScoreChange(...) { /* ... */ }
// ... implement all methods

engine := scoring.NewEngine(
    scoring.WithMetrics(&MyRecorder{}),
)

// Combined with persistence
engine := scoring.NewEngine(
    scoring.WithStore(
        store.WithRedisStore("localhost:6379"),
    ),
    scoring.WithPrometheusMetrics(),
)
```

### Engine Integration

The engine calls metrics recorder methods at appropriate points:

```go
// In engine.go - after recording an event
func (e *Engine) RecordEvent(event ScoreEvent) {
    // ... existing logic ...

    // Record event metric
    e.metrics.RecordEvent(ctx, event.Provider, string(event.Type), event.Error == nil)

    // Record latency if available
    if event.ResponseTime > 0 {
        e.metrics.RecordLatency(ctx, event.Provider, string(event.Type), event.ResponseTime)
    }

    // Record score changes
    oldScore := score.EffectiveScore()
    // ... apply penalties ...
    newScore := score.EffectiveScore()

    e.metrics.RecordScoreChange(ctx, event.Provider, metrics.ScoreTypeEffective, oldScore, newScore)
    e.metrics.RecordEffectiveScore(ctx, event.Provider, newScore)
}

// In engine.go - after sorting providers
func (e *Engine) GetSortedProviders() []string {
    // ... existing logic ...

    // Record provider ranks
    for i, name := range names {
        e.metrics.RecordProviderRank(context.Background(), name, i+1, len(names))
    }

    return names
}

// In store implementations - wrap operations
func (r *RedisStore) GetScore(ctx context.Context, name string) (*ProviderScoreData, error) {
    start := time.Now()
    data, err := r.getScoreInternal(ctx, name)
    r.metrics.RecordStoreOperation(ctx, "redis", "get_score", time.Since(start), err)
    return data, err
}
```

### Tracing Support (Optional Extension)

For distributed tracing, the interface can be extended:

```go
// scoring/metrics/tracing.go

package metrics

import (
    "context"
)

// Tracer provides distributed tracing capabilities
type Tracer interface {
    // StartSpan starts a new span for an operation
    StartSpan(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span)
}

// Span represents a single operation span
type Span interface {
    End()
    SetAttributes(attrs ...Attribute)
    RecordError(err error)
}

// SpanOption configures span creation
type SpanOption func(*SpanConfig)

// Combined recorder with tracing
type InstrumentedRecorder struct {
    Recorder
    Tracer Tracer
}
```

### File Structure (Updated)

```
chainkit/scoring/
├── metrics/
│   ├── metrics.go           # Core Recorder interface
│   ├── noop.go              # No-op implementation (default)
│   ├── prometheus/
│   │   └── prometheus.go    # Prometheus implementation
│   ├── otel/
│   │   └── otel.go          # OpenTelemetry implementation
│   └── tracing.go           # Optional tracing interface
├── store/
│   └── ...
├── engine.go
└── ...
```

---

## Configuration API

### Functional Options

```go
// scoring/store/options.go

package store

import "time"

// StoreConfig holds configuration for creating stores
type StoreConfig struct {
    Type StoreType

    // Memory options (none needed)

    // Redis options
    Redis *RedisConfig

    // Postgres options
    Postgres *PostgresConfig

    // Hybrid options
    Hybrid *HybridConfig
}

// StoreOption is a functional option for store configuration
type StoreOption func(*StoreConfig)

// WithMemoryStore configures in-memory storage (default)
func WithMemoryStore() StoreOption {
    return func(c *StoreConfig) {
        c.Type = StoreTypeMemory
    }
}

// WithRedisStore configures Redis storage
func WithRedisStore(addr string, opts ...func(*RedisConfig)) StoreOption {
    return func(c *StoreConfig) {
        c.Type = StoreTypeRedis
        c.Redis = &RedisConfig{
            Addr:     addr,
            PoolSize: 10,
        }
        for _, opt := range opts {
            opt(c.Redis)
        }
    }
}

// Redis-specific options
func RedisPassword(password string) func(*RedisConfig) {
    return func(c *RedisConfig) { c.Password = password }
}

func RedisDB(db int) func(*RedisConfig) {
    return func(c *RedisConfig) { c.DB = db }
}

func RedisScoreTTL(ttl time.Duration) func(*RedisConfig) {
    return func(c *RedisConfig) { c.ScoreTTL = ttl }
}

func RedisKeyPrefix(prefix string) func(*RedisConfig) {
    return func(c *RedisConfig) { c.KeyPrefix = prefix }
}

// WithPostgresStore configures PostgreSQL storage
func WithPostgresStore(connString string, opts ...func(*PostgresConfig)) StoreOption {
    return func(c *StoreConfig) {
        c.Type = StoreTypePostgres
        c.Postgres = &PostgresConfig{
            ConnectionString: connString,
            MaxOpenConns:     25,
            MaxIdleConns:     5,
        }
        for _, opt := range opts {
            opt(c.Postgres)
        }
    }
}

// Postgres-specific options
func PostgresTablePrefix(prefix string) func(*PostgresConfig) {
    return func(c *PostgresConfig) { c.TablePrefix = prefix }
}

func PostgresMaxConns(max int) func(*PostgresConfig) {
    return func(c *PostgresConfig) { c.MaxOpenConns = max }
}

// WithHybridStore configures hybrid (database + cache) storage
func WithHybridStore(primary, cache StoreOption, opts ...func(*HybridConfig)) StoreOption {
    return func(c *StoreConfig) {
        c.Type = StoreTypeHybrid

        // Create primary config
        primaryConfig := &StoreConfig{}
        primary(primaryConfig)

        // Create cache config
        cacheConfig := &StoreConfig{}
        cache(cacheConfig)

        c.Hybrid = &HybridConfig{
            // Primary and Cache will be created during NewStore
            CacheTTL:     5 * time.Minute,
            WriteThrough: true,
        }

        for _, opt := range opts {
            opt(c.Hybrid)
        }
    }
}

// Hybrid-specific options
func HybridCacheTTL(ttl time.Duration) func(*HybridConfig) {
    return func(c *HybridConfig) { c.CacheTTL = ttl }
}

func HybridAsyncWrite(async bool) func(*HybridConfig) {
    return func(c *HybridConfig) { c.AsyncWrite = async }
}
```

### Engine Integration

```go
// Updated scoring/options.go

// WithStore sets the storage backend for the scoring engine
func WithStore(opts ...store.StoreOption) ScoringOption {
    return func(c *ScoringConfig) {
        storeConfig := &store.StoreConfig{
            Type: store.StoreTypeMemory, // Default
        }
        for _, opt := range opts {
            opt(storeConfig)
        }
        c.StoreConfig = storeConfig
    }
}
```

### Usage Examples

```go
// Memory only (default)
engine := scoring.NewEngine()

// Redis
engine := scoring.NewEngine(
    scoring.WithStore(
        store.WithRedisStore("localhost:6379",
            store.RedisPassword("secret"),
            store.RedisScoreTTL(24*time.Hour),
        ),
    ),
)

// PostgreSQL
engine := scoring.NewEngine(
    scoring.WithStore(
        store.WithPostgresStore("postgres://user:pass@localhost/db",
            store.PostgresTablePrefix("myapp_"),
        ),
    ),
)

// Hybrid (Postgres + Redis cache)
engine := scoring.NewEngine(
    scoring.WithStore(
        store.WithHybridStore(
            store.WithPostgresStore("postgres://..."),
            store.WithRedisStore("localhost:6379"),
            store.HybridCacheTTL(5*time.Minute),
            store.HybridAsyncWrite(true),
        ),
    ),
)
```

---

## Data Model

### Database Schema

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
    recent_latencies    JSONB,
    created_at          TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at          TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Latency statistics table
CREATE TABLE chainkit_latency_stats (
    id                  SERIAL PRIMARY KEY,
    provider_samples    JSONB NOT NULL,
    updated_at          TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Indexes
CREATE INDEX idx_provider_scores_updated ON chainkit_provider_scores(updated_at);

-- Optional: Score history for analytics
CREATE TABLE chainkit_score_history (
    id                  BIGSERIAL PRIMARY KEY,
    provider_name       VARCHAR(255) NOT NULL,
    effective_score     DOUBLE PRECISION NOT NULL,
    health_penalty      DOUBLE PRECISION NOT NULL,
    latency_penalty     DOUBLE PRECISION NOT NULL,
    error_penalty       DOUBLE PRECISION NOT NULL,
    rate_limit_penalty  DOUBLE PRECISION NOT NULL,
    recorded_at         TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_score_history_provider ON chainkit_score_history(provider_name, recorded_at);
```

### Redis Key Structure

```
chainkit:scoring:score:{provider_name}     -> JSON ProviderScoreData
chainkit:scoring:latency                   -> JSON LatencyStatsData
chainkit:scoring:providers                 -> SET of provider names
chainkit:scoring:lock:{provider_name}      -> Lock value (for distributed locking)
chainkit:scoring:events                    -> PUB/SUB channel for updates
```

---

## Migration & Backwards Compatibility

### Backwards Compatibility

1. **Default behavior unchanged**: Memory store is default
2. **No required config changes**: Existing code works without modification
3. **Optional persistence**: Users opt-in to persistence

### Migration from Memory to Persistent

```go
// Export current scores to persistent store
func MigrateScores(ctx context.Context, engine *scoring.Engine, target store.ScoreStore) error {
    stats := engine.GetAllProviderStats()

    data := make([]*store.ProviderScoreData, len(stats))
    for i, s := range stats {
        data[i] = &store.ProviderScoreData{
            Name:             s.Name,
            BaseScore:        s.BaseScore,
            HealthPenalty:    s.HealthPenalty,
            LatencyPenalty:   s.LatencyPenalty,
            ErrorPenalty:     s.ErrorPenalty,
            RateLimitPenalty: s.RateLimitPenalty,
            TotalOperations:  s.TotalOperations,
            SuccessfulOps:    s.SuccessfulOps,
            FailedOps:        s.FailedOps,
            LastUpdated:      s.LastUpdated,
        }
    }

    return target.SetScores(ctx, data)
}
```

### Schema Migrations

```go
// scoring/store/migrations.go

type Migration struct {
    Version     int
    Description string
    Up          func(ctx context.Context, db *sql.DB) error
    Down        func(ctx context.Context, db *sql.DB) error
}

var migrations = []Migration{
    {
        Version:     1,
        Description: "Create provider_scores table",
        Up: func(ctx context.Context, db *sql.DB) error {
            _, err := db.ExecContext(ctx, `
                CREATE TABLE IF NOT EXISTS chainkit_provider_scores (...)
            `)
            return err
        },
    },
    {
        Version:     2,
        Description: "Add score history table",
        Up: func(ctx context.Context, db *sql.DB) error {
            _, err := db.ExecContext(ctx, `
                CREATE TABLE IF NOT EXISTS chainkit_score_history (...)
            `)
            return err
        },
    },
}
```

---

## Performance Considerations

### Write Batching

To reduce database/Redis load, batch writes:

```go
type BatchingStore struct {
    inner       ScoreStore
    buffer      []*ProviderScoreData
    bufferSize  int
    flushTicker *time.Ticker
    mu          sync.Mutex
}

func (b *BatchingStore) SetScore(ctx context.Context, data *ProviderScoreData) error {
    b.mu.Lock()
    b.buffer = append(b.buffer, data)

    if len(b.buffer) >= b.bufferSize {
        batch := b.buffer
        b.buffer = nil
        b.mu.Unlock()
        return b.inner.SetScores(ctx, batch)
    }

    b.mu.Unlock()
    return nil
}
```

### Sync Intervals

Configure how often to persist:

```go
type SyncConfig struct {
    // SyncInterval defines how often to persist scores to storage
    SyncInterval time.Duration // Default: 10 seconds

    // SyncOnEvent persists immediately after each event
    SyncOnEvent bool // Default: false

    // SyncBatchSize maximum scores to sync at once
    SyncBatchSize int // Default: 100
}
```

### Read Caching

For high-read scenarios:

```go
type CachedStore struct {
    inner       ScoreStore
    cache       *lru.Cache
    ttl         time.Duration
}

func (c *CachedStore) GetScore(ctx context.Context, name string) (*ProviderScoreData, error) {
    if cached, ok := c.cache.Get(name); ok {
        return cached.(*ProviderScoreData), nil
    }

    data, err := c.inner.GetScore(ctx, name)
    if err == nil && data != nil {
        c.cache.Add(name, data)
    }
    return data, err
}
```

---

## Implementation Phases

### Phase 1: Core Interface & Memory Store (1-2 days)

- [ ] Define `ScoreStore` interface
- [ ] Implement `MemoryStore`
- [ ] Update `Engine` to use `ScoreStore`
- [ ] Add store configuration options
- [ ] Maintain backwards compatibility

### Phase 2: Redis Store (2-3 days)

- [ ] Implement `RedisStore`
- [ ] Add Redis configuration options
- [ ] Implement pub/sub for distributed updates
- [ ] Add distributed locking
- [ ] Write integration tests

### Phase 3: PostgreSQL Store (2-3 days)

- [ ] Implement `PostgresStore`
- [ ] Create database migrations
- [ ] Add batch operations
- [ ] Add connection pooling
- [ ] Write integration tests

### Phase 4: Hybrid Store (1-2 days)

- [ ] Implement `HybridStore`
- [ ] Add cache invalidation strategies
- [ ] Add write-through/write-behind options
- [ ] Write integration tests

### Phase 5: Metrics & Observability (2-3 days)

- [ ] Define `Recorder` interface
- [ ] Implement `NoOpRecorder`
- [ ] Implement Prometheus recorder
- [ ] Implement OpenTelemetry recorder
- [ ] Integrate metrics into engine
- [ ] Integrate metrics into stores
- [ ] Add metrics configuration options
- [ ] Write tests for all recorders

### Phase 6: Advanced Features (2-3 days)

- [ ] Write batching for performance
- [ ] Score history/analytics table
- [ ] Migration utilities
- [ ] Documentation

### Phase 7: Testing & Documentation (1-2 days)

- [ ] Unit tests for all stores
- [ ] Unit tests for all metrics recorders
- [ ] Integration tests with real Redis/Postgres
- [ ] Performance benchmarks
- [ ] Update README and examples
- [ ] Add migration guide
- [ ] Add observability guide

---

## Testing Strategy

### Unit Tests

```go
// Test all stores implement the interface correctly
func TestStoreInterface(t *testing.T) {
    stores := []struct {
        name  string
        store store.ScoreStore
    }{
        {"memory", store.NewMemoryStore()},
        // Add Redis and Postgres with test containers
    }

    for _, s := range stores {
        t.Run(s.name, func(t *testing.T) {
            testStoreBasicOperations(t, s.store)
            testStoreBatchOperations(t, s.store)
            testStoreConcurrency(t, s.store)
        })
    }
}
```

### Integration Tests

```go
// Use testcontainers for real Redis/Postgres
func TestRedisIntegration(t *testing.T) {
    ctx := context.Background()

    container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
        ContainerRequest: testcontainers.ContainerRequest{
            Image:        "redis:7",
            ExposedPorts: []string{"6379/tcp"},
        },
        Started: true,
    })
    require.NoError(t, err)
    defer container.Terminate(ctx)

    host, _ := container.Host(ctx)
    port, _ := container.MappedPort(ctx, "6379")

    store, err := store.NewRedisStore(store.RedisConfig{
        Addr: fmt.Sprintf("%s:%s", host, port.Port()),
    })
    require.NoError(t, err)

    // Run tests...
}
```

### Benchmarks

```go
func BenchmarkStoreSetScore(b *testing.B) {
    stores := map[string]store.ScoreStore{
        "memory":   store.NewMemoryStore(),
        // Add others...
    }

    for name, s := range stores {
        b.Run(name, func(b *testing.B) {
            data := &store.ProviderScoreData{
                Name:      "test",
                BaseScore: 100,
            }

            for i := 0; i < b.N; i++ {
                s.SetScore(context.Background(), data)
            }
        })
    }
}
```

---

## File Structure

```
chainkit/scoring/
├── store/
│   ├── store.go           # ScoreStore interface
│   ├── registry.go        # Store factory and registration
│   ├── options.go         # Configuration options
│   ├── memory.go          # In-memory implementation
│   ├── redis.go           # Redis implementation
│   ├── postgres.go        # PostgreSQL implementation
│   ├── hybrid.go          # Hybrid (DB + cache) implementation
│   ├── batching.go        # Write batching wrapper
│   ├── migrations.go      # Database migrations
│   ├── store_test.go      # Unit tests
│   ├── redis_test.go      # Redis integration tests
│   ├── postgres_test.go   # Postgres integration tests
│   └── benchmark_test.go  # Performance benchmarks
├── engine.go              # Updated to use ScoreStore
├── options.go             # Updated with WithStore option
└── ...
```

---

## Summary

This plan provides a clean, extensible architecture for adding persistence to the scoring engine:

| Feature        | Memory | Redis | PostgreSQL | Hybrid |
| -------------- | ------ | ----- | ---------- | ------ |
| Persistence    | ❌     | ⚡    | ✅         | ✅     |
| Multi-instance | ❌     | ✅    | ✅         | ✅     |
| Performance    | ⚡⚡⚡ | ⚡⚡  | ⚡         | ⚡⚡   |
| Dependencies   | None   | Redis | PostgreSQL | Both   |
| Audit trail    | ❌     | ❌    | ✅         | ✅     |

### Metrics Comparison

| Feature             | No-Op | Prometheus               | OpenTelemetry            |
| ------------------- | ----- | ------------------------ | ------------------------ |
| Dependencies        | None  | prometheus/client_golang | go.opentelemetry.io/otel |
| Overhead            | Zero  | Minimal                  | Minimal                  |
| Pull-based          | N/A   | ✅                       | Configurable             |
| Push-based          | N/A   | Via Pushgateway          | ✅                       |
| Distributed tracing | ❌    | ❌                       | ✅                       |
| OTLP export         | ❌    | ❌                       | ✅                       |

The implementation follows chainkit's existing patterns (functional options, interfaces) and maintains full backwards compatibility. Metrics are opt-in with zero overhead when not configured.
