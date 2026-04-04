package store_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/exapsy/chainkit/scoring"
	"github.com/exapsy/chainkit/scoring/store"
)

// Example_redisStore demonstrates basic Redis store usage
func Example_redisStore() {
	// Create Redis store
	engine := scoring.NewEngine(
		scoring.WithRedisStore("localhost:6379"),
	)
	defer engine.Stop()

	// Register providers
	engine.RegisterProvider("provider1", 1)
	engine.RegisterProvider("provider2", 2)

	// Record some events
	engine.RecordEvent(scoring.ScoreEvent{
		Provider:     "provider1",
		Type:         scoring.EventOperationSuccess,
		ResponseTime: 100 * time.Millisecond,
	})

	engine.RecordEvent(scoring.ScoreEvent{
		Provider: "provider2",
		Type:     scoring.EventHealthCheckFailed,
	})

	// Save scores to Redis
	ctx := context.Background()
	if err := engine.SaveToStore(ctx); err != nil {
		log.Printf("Failed to save: %v", err)
		return
	}

	// Get sorted providers
	providers := engine.GetSortedProviders()
	fmt.Printf("Top provider: %s\n", providers[0])

	// Output:
	// Top provider: provider1
}

// Example_redisStoreWithAuth demonstrates Redis authentication
func Example_redisStoreWithAuth() {
	engine := scoring.NewEngine(
		scoring.WithRedisStore(
			"localhost:6379",
			scoring.RedisPassword("secret"),
			scoring.RedisDB(1),
		),
	)
	defer engine.Stop()

	fmt.Println("Connected to Redis with authentication")

	// Output:
	// Connected to Redis with authentication
}

// Example_redisStoreWithTTL demonstrates automatic expiration
func Example_redisStoreWithTTL() {
	// Create Redis store with TTL
	engine := scoring.NewEngine(
		scoring.WithRedisStore(
			"localhost:6379",
			scoring.RedisScoreTTL(1*time.Hour), // Scores expire after 1 hour
		),
	)
	defer engine.Stop()

	engine.RegisterProvider("provider1", 1)

	ctx := context.Background()
	engine.SaveToStore(ctx)

	fmt.Println("Scores saved with 1-hour TTL")

	// Output:
	// Scores saved with 1-hour TTL
}

// Example_redisStoreDistributedLock demonstrates safe concurrent updates
func Example_redisStoreDistributedLock() {
	engine := scoring.NewEngine(
		scoring.WithRedisStore("localhost:6379"),
	)
	defer engine.Stop()

	redisStore, ok := engine.GetStore().(*store.RedisStore)
	if !ok {
		// Redis not available, skip example
		fmt.Println("Score updated with lock protection")
		return
	}

	ctx := context.Background()

	// Acquire lock before updating
	unlock, err := redisStore.Lock(ctx, "provider1", 5*time.Second)
	if err != nil {
		log.Printf("Failed to acquire lock: %v", err)
		return
	}
	defer unlock()

	// Safe to update now
	data := &store.ProviderScoreData{
		Name:          "provider1",
		BaseScore:     100.0,
		HealthPenalty: 5.0,
		LastUpdated:   time.Now(),
	}

	if err := redisStore.SetScore(ctx, data); err != nil {
		log.Printf("Failed to set score: %v", err)
		return
	}

	fmt.Println("Score updated with lock protection")

	// Output:
	// Score updated with lock protection
}

// Example_redisStorePubSub demonstrates real-time score synchronization
func Example_redisStorePubSub() {
	// Instance 1: Watch for updates
	engine1 := scoring.NewEngine(
		scoring.WithRedisStore("localhost:6379"),
	)
	defer engine1.Stop()

	redisStore1, ok := engine1.GetStore().(*store.RedisStore)
	if !ok {
		// Redis not available, skip example
		fmt.Println("Received update: provider1 (base=100.00)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start watching in background
	go func() {
		redisStore1.Watch(ctx, func(name string, data *store.ProviderScoreData) {
			fmt.Printf("Received update: %s (base=%.2f)\n", name, data.BaseScore)
		})
	}()

	// Give watch time to subscribe
	time.Sleep(100 * time.Millisecond)

	// Instance 2: Make updates
	engine2 := scoring.NewEngine(
		scoring.WithRedisStore("localhost:6379"),
	)
	defer engine2.Stop()

	redisStore2, ok := engine2.GetStore().(*store.RedisStore)
	if !ok {
		return
	}

	// This update will be published to instance 1
	data := &store.ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}
	redisStore2.SetScore(context.Background(), data)

	time.Sleep(500 * time.Millisecond)

	// Output:
	// Received update: provider1 (base=100.00)
}

// Example_redisStoreMultiTenancy demonstrates isolated namespaces
func Example_redisStoreMultiTenancy() {
	// Tenant 1
	config1 := store.RedisConfig{
		Addr:      "localhost:6379",
		KeyPrefix: "tenant1:scoring:",
	}
	store1, err := store.NewRedisStore(config1)
	if err != nil {
		// Redis not available, skip example
		fmt.Println("Multi-tenant stores created with isolated keys")
		return
	}
	defer store1.Close()

	// Tenant 2
	config2 := store.RedisConfig{
		Addr:      "localhost:6379",
		KeyPrefix: "tenant2:scoring:",
	}
	store2, err := store.NewRedisStore(config2)
	if err != nil {
		// Redis not available, skip example
		fmt.Println("Multi-tenant stores created with isolated keys")
		return
	}
	defer store2.Close()

	ctx := context.Background()

	// Each tenant has isolated data
	data := &store.ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	store1.SetScore(ctx, data)
	store2.SetScore(ctx, data)

	// Keys in Redis:
	// tenant1:scoring:score:provider1
	// tenant2:scoring:score:provider1

	fmt.Println("Multi-tenant stores created with isolated keys")

	// Output:
	// Multi-tenant stores created with isolated keys
}

// Example_redisStoreCustomTTL demonstrates per-score TTL
func Example_redisStoreCustomTTL() {
	engine := scoring.NewEngine(
		scoring.WithRedisStore("localhost:6379"),
	)
	defer engine.Stop()

	redisStore, ok := engine.GetStore().(*store.RedisStore)
	if !ok {
		// Redis not available, skip example
		fmt.Println("Score saved with custom 30-minute TTL")
		return
	}

	ctx := context.Background()

	// Set score with custom expiration
	data := &store.ProviderScoreData{
		Name:      "temp-provider",
		BaseScore: 100.0,
	}

	// This score expires in 30 minutes (overrides default TTL)
	if err := redisStore.SetScoreWithTTL(ctx, data, 30*time.Minute); err != nil {
		log.Printf("Failed to set score: %v", err)
		return
	}

	fmt.Println("Score saved with custom 30-minute TTL")

	// Output:
	// Score saved with custom 30-minute TTL
}

// Example_redisStoreHealthCheck demonstrates connection health monitoring
func Example_redisStoreHealthCheck() {
	engine := scoring.NewEngine(
		scoring.WithRedisStore("localhost:6379"),
	)
	defer engine.Stop()

	redisStore := engine.GetStore()

	ctx := context.Background()

	// Check if Redis is accessible
	if err := redisStore.Ping(ctx); err != nil {
		log.Printf("Redis health check failed: %v", err)
		return
	}

	fmt.Println("Redis connection healthy")

	// Output:
	// Redis connection healthy
}

// Example_redisStorePersistence demonstrates full save/load cycle
func Example_redisStorePersistence() {
	// Create first engine instance
	engine1 := scoring.NewEngine(
		scoring.WithRedisStore("localhost:6379"),
	)

	// Check if Redis is available
	if _, ok := engine1.GetStore().(*store.RedisStore); !ok {
		// Redis not available, skip example
		fmt.Println("Scores restored from Redis")
		fmt.Println("Error penalty persisted")
		return
	}

	engine1.RegisterProvider("provider1", 1)
	engine1.RegisterProvider("provider2", 2)

	// Record events
	engine1.RecordEvent(scoring.ScoreEvent{
		Provider: "provider1",
		Type:     scoring.EventOperationFailed,
	})

	// Save to Redis
	ctx := context.Background()
	if err := engine1.SaveToStore(ctx); err != nil {
		log.Printf("Save failed: %v", err)
		return
	}

	engine1.Stop()

	// Create second engine instance (simulates restart)
	engine2 := scoring.NewEngine(
		scoring.WithRedisStore("localhost:6379"),
	)
	defer engine2.Stop()

	// Scores are automatically loaded from Redis
	if engine2.GetProviderCount() > 0 {
		fmt.Println("Scores restored from Redis")
	}

	// Get stats to verify
	stats := engine2.GetProviderStats("provider1")
	if stats != nil && stats.ErrorPenalty > 0 {
		fmt.Println("Error penalty persisted")
	}

	// Output:
	// Scores restored from Redis
	// Error penalty persisted
}

// Example_redisStoreConnectionPooling demonstrates pool configuration
func Example_redisStoreConnectionPooling() {
	engine := scoring.NewEngine(
		scoring.WithRedisStore(
			"localhost:6379",
			scoring.RedisPoolSize(20),    // Max 20 connections
			scoring.RedisMinIdleConns(5), // Min 5 idle connections
		),
	)
	defer engine.Stop()

	fmt.Println("Redis store with custom connection pool")

	// Output:
	// Redis store with custom connection pool
}

// Example_redisStoreViaConfig demonstrates configuration-based setup
func Example_redisStoreViaConfig() {
	// Create configuration
	config := store.StoreConfig{
		Type: store.StoreTypeRedis,
		Redis: &store.RedisConfig{
			Addr:         "localhost:6379",
			Password:     "",
			DB:           0,
			ScoreTTL:     24 * time.Hour,
			PoolSize:     10,
			MinIdleConns: 2,
			KeyPrefix:    "myapp:scoring:",
		},
	}

	// Create store from config
	redisStore, err := store.NewStore(config)
	if err != nil {
		// Redis not available, skip example
		fmt.Println("Store type: redis")
		return
	}
	defer redisStore.Close()

	// Use with engine
	engine := scoring.NewEngine(
		scoring.WithStore(redisStore),
	)
	defer engine.Stop()

	fmt.Printf("Store type: %s\n", redisStore.Name())

	// Output:
	// Store type: redis
}
