# Redis Store Quick Start Guide

Get started with distributed provider scoring using Redis in under 5 minutes.

## Prerequisites

- **Redis Server** (local or remote)
- **Go 1.21+**
- **Chainkit** with scoring package

## Installation

The Redis store is built-in. Just import and use:

```go
import (
    "github.com/exapsy/chainkit/scoring"
)
```

Dependencies are automatically managed via `go.mod`.

## Quick Start

### 1. Basic Setup (No Authentication)

```go
package main

import (
    "context"
    "log"
    "time"
    
    "github.com/exapsy/chainkit/scoring"
)

func main() {
    // Create engine with Redis store
    engine := scoring.NewEngine(
        scoring.WithRedisStore("localhost:6379"),
    )
    defer engine.Stop()
    
    // Register providers
    engine.RegisterProvider("mempool", 1)
    engine.RegisterProvider("blockcypher", 2)
    
    // Start background processes
    ctx := context.Background()
    engine.Start(ctx)
    
    // Record events - scores automatically saved to Redis
    engine.RecordEvent(scoring.ScoreEvent{
        Provider:     "mempool",
        Type:         scoring.EventOperationSuccess,
        ResponseTime: 100 * time.Millisecond,
    })
    
    // Save scores to Redis
    if err := engine.SaveToStore(ctx); err != nil {
        log.Printf("Save failed: %v", err)
    }
    
    log.Println("Scores saved to Redis!")
}
```

### 2. Production Setup (With Authentication)

```go
engine := scoring.NewEngine(
    scoring.WithRedisStore(
        "redis.example.com:6379",
        scoring.RedisPassword("your-secure-password"),
        scoring.RedisDB(1),                        // Use database 1
        scoring.RedisScoreTTL(24 * time.Hour),     // Auto-expire after 24h
        scoring.RedisPoolSize(20),                 // Max 20 connections
    ),
)
```

### 3. Multi-Instance Setup (Kubernetes, Auto-Scaling)

```go
// All instances share the same Redis
engine := scoring.NewEngine(
    scoring.WithRedisStore(
        "redis-service:6379",
        scoring.RedisKeyPrefix("app:scoring:"),   // Namespace your keys
    ),
)

// Automatically syncs across all instances!
```

## Common Use Cases

### Use Case 1: Save Scores Periodically

```go
// Save scores every minute
ticker := time.NewTicker(1 * time.Minute)
defer ticker.Stop()

for range ticker.C {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    if err := engine.SaveToStore(ctx); err != nil {
        log.Printf("Failed to save: %v", err)
    }
    cancel()
}
```

### Use Case 2: Real-Time Score Updates Across Instances

```go
import "github.com/exapsy/chainkit/scoring/store"

// Instance 1: Watch for updates
redisStore := engine.GetStore().(*store.RedisStore)

go func() {
    ctx := context.Background()
    redisStore.Watch(ctx, func(name string, data *store.ProviderScoreData) {
        log.Printf("Provider %s updated: base=%.2f", name, data.BaseScore)
        // Reload engine state
        engine.LoadFromStore(ctx)
    })
}()

// Instance 2: Updates automatically broadcast to watchers
engine.RecordEvent(scoring.ScoreEvent{...})
engine.SaveToStore(ctx)
```

### Use Case 3: Safe Concurrent Updates with Locking

```go
redisStore := engine.GetStore().(*store.RedisStore)

// Acquire distributed lock
unlock, err := redisStore.Lock(ctx, "provider1", 5*time.Second)
if err != nil {
    log.Printf("Lock failed: %v", err)
    return
}
defer unlock()

// Update score safely
data := &store.ProviderScoreData{
    Name:      "provider1",
    BaseScore: 100.0,
}
redisStore.SetScore(ctx, data)
```

### Use Case 4: Graceful Shutdown (Save Before Exit)

```go
import (
    "os"
    "os/signal"
    "syscall"
)

sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

<-sigChan
log.Println("Shutting down...")

// Save scores before exit
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := engine.SaveToStore(ctx); err != nil {
    log.Printf("Warning: failed to save on shutdown: %v", err)
}

engine.Stop()
log.Println("Goodbye!")
```

## Testing

### Local Redis with Docker

```bash
# Start Redis
docker run -d -p 6379:6379 redis:7-alpine

# Run your app
go run main.go
```

### Unit Tests (Skip Redis)

```bash
# Run unit tests only (no Redis required)
go test -short ./...
```

### Integration Tests (With Redis)

```bash
# Run all tests including Redis integration
go test ./scoring/store/...
```

## Configuration Options

| Option                    | Description                    | Default      |
| ------------------------- | ------------------------------ | ------------ |
| `RedisPassword(pwd)`      | Redis password                 | "" (none)    |
| `RedisDB(db)`             | Database number (0-15)         | 0            |
| `RedisScoreTTL(ttl)`      | Auto-expiration time           | 0 (never)    |
| `RedisKeyPrefix(prefix)`  | Key namespace                  | "chainkit:*" |
| `RedisPoolSize(size)`     | Max connections                | 10           |
| `RedisMinIdleConns(conns)` | Min idle connections          | 2            |

## Troubleshooting

### "Connection refused"

**Cause:** Redis server not running or wrong address

**Fix:**
```bash
# Check Redis is running
redis-cli ping
# Should return: PONG

# Or start Redis
docker run -d -p 6379:6379 redis:7-alpine
```

### "NOAUTH Authentication required"

**Cause:** Redis requires password but none provided

**Fix:**
```go
engine := scoring.NewEngine(
    scoring.WithRedisStore(
        "localhost:6379",
        scoring.RedisPassword("your-password"),
    ),
)
```

### "Lock already held"

**Cause:** Another instance holds the distributed lock

**Fix:** This is normal. Wait and retry, or increase lock TTL:
```go
unlock, err := redisStore.Lock(ctx, "provider1", 10*time.Second) // Longer TTL
```

### Scores not persisting across restarts

**Cause:** Not calling `SaveToStore()` or Redis persistence disabled

**Fix:**
```go
// Save periodically
ticker := time.NewTicker(1 * time.Minute)
for range ticker.C {
    engine.SaveToStore(ctx)
}

// AND enable Redis persistence (redis.conf)
# appendonly yes
```

## Production Checklist

- [ ] Redis authentication enabled (`RedisPassword()`)
- [ ] Connection pooling tuned (`RedisPoolSize()`)
- [ ] TTL configured for auto-cleanup (`RedisScoreTTL()`)
- [ ] Key prefix set for multi-tenancy (`RedisKeyPrefix()`)
- [ ] Periodic saves implemented (ticker or on events)
- [ ] Graceful shutdown saves scores
- [ ] Redis persistence enabled (RDB or AOF)
- [ ] Redis monitoring configured (memory, connections)
- [ ] High availability considered (Redis Sentinel/Cluster)

## Next Steps

1. **Phase 3 (PostgreSQL):** Add durable database storage for long-term persistence
2. **Phase 4 (Hybrid):** Combine Redis (cache) + PostgreSQL (primary) for best performance
3. **Phase 5 (Metrics):** Add Prometheus/OpenTelemetry instrumentation

## Learn More

- **Full Documentation:** [store/README.md](./store/README.md)
- **Implementation Details:** [PHASE2_COMPLETE.md](./PHASE2_COMPLETE.md)
- **API Reference:** [GoDoc](https://pkg.go.dev/github.com/exapsy/chainkit/scoring)
- **Examples:** [store/example_redis_test.go](./store/example_redis_test.go)

## Support

- **GitHub Issues:** [exapsy/chainkit/issues](https://github.com/exapsy/chainkit/issues)
- **Persistence Plan:** [PERSISTENCE_PLAN.md](./PERSISTENCE_PLAN.md)

---

**Happy Distributed Scoring! 🚀**
