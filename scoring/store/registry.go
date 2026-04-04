package store

import (
	"fmt"
	"time"
)

// StoreType represents the type of storage backend.
type StoreType string

const (
	// StoreTypeMemory uses in-memory storage (default, single-instance only).
	StoreTypeMemory StoreType = "memory"

	// StoreTypeRedis uses Redis for distributed storage (planned).
	StoreTypeRedis StoreType = "redis"

	// StoreTypePostgres uses PostgreSQL for durable storage (planned).
	StoreTypePostgres StoreType = "postgres"

	// StoreTypeHybrid uses a combination of cache and primary storage (planned).
	StoreTypeHybrid StoreType = "hybrid"
)

// StoreConfig holds configuration for creating a score store.
type StoreConfig struct {
	// Type specifies which store implementation to use.
	Type StoreType

	// Redis configuration (used when Type == StoreTypeRedis)
	Redis *RedisConfig

	// Postgres configuration (used when Type == StoreTypePostgres)
	Postgres *PostgresConfig

	// Hybrid configuration (used when Type == StoreTypeHybrid)
	Hybrid *HybridStoreConfig
}

// RedisConfig holds configuration for Redis-based storage.
type RedisConfig struct {
	// Addr is the Redis server address (host:port).
	Addr string

	// Password for Redis authentication (optional).
	Password string

	// DB is the Redis database number (0-15).
	DB int

	// PoolSize is the maximum number of socket connections.
	PoolSize int

	// MinIdleConns is the minimum number of idle connections.
	MinIdleConns int

	// ScoreTTL is the time-to-live for score data (0 = no expiration).
	ScoreTTL time.Duration

	// KeyPrefix is prepended to all Redis keys for namespacing.
	KeyPrefix string
}

// PostgresConfig holds configuration for PostgreSQL-based storage.
type PostgresConfig struct {
	// ConnectionString is the PostgreSQL connection string.
	ConnectionString string

	// TablePrefix is prepended to all table names for namespacing.
	TablePrefix string

	// MaxOpenConns is the maximum number of open connections.
	MaxOpenConns int

	// MaxIdleConns is the maximum number of idle connections.
	MaxIdleConns int

	// ConnMaxLifetime is the maximum lifetime of a connection.
	ConnMaxLifetime time.Duration
}

// HybridStoreConfig holds configuration for hybrid (cache + primary) storage.
// This is used by the factory pattern to create sub-stores.
type HybridStoreConfig struct {
	// Primary is the primary store configuration (typically Postgres).
	Primary StoreConfig

	// Cache is the cache store configuration (typically Redis or Memory).
	Cache StoreConfig

	// CacheTTL is how long to cache data before refreshing from primary.
	CacheTTL time.Duration

	// WriteThrough enables synchronous writes to both cache and primary.
	WriteThrough bool

	// AsyncWrite enables asynchronous writes to cache (only if WriteThrough=true).
	AsyncWrite bool

	// InvalidateOnWrite invalidates cache entries on write instead of updating.
	InvalidateOnWrite bool
}

// StoreFactory is a function that creates a ScoreStore from configuration.
type StoreFactory func(config StoreConfig) (ScoreStore, error)

// Global registry of store factories.
var factories = make(map[StoreType]StoreFactory)

// Register registers a store factory for a specific store type.
// This allows custom store implementations to be plugged in.
func Register(storeType StoreType, factory StoreFactory) {
	factories[storeType] = factory
}

// NewStore creates a ScoreStore based on the provided configuration.
// Returns an error if the store type is unknown or creation fails.
func NewStore(config StoreConfig) (ScoreStore, error) {
	factory, ok := factories[config.Type]
	if !ok {
		return nil, fmt.Errorf("unknown store type: %s", config.Type)
	}
	return factory(config)
}

// init registers the built-in store types.
func init() {
	// Register the memory store (always available)
	Register(StoreTypeMemory, func(config StoreConfig) (ScoreStore, error) {
		return NewMemoryStore(), nil
	})

	// Register the Redis store
	Register(StoreTypeRedis, func(config StoreConfig) (ScoreStore, error) {
		if config.Redis == nil {
			return nil, fmt.Errorf("redis config is required for redis store")
		}
		return NewRedisStore(*config.Redis)
	})

	// Register the Postgres store
	Register(StoreTypePostgres, func(config StoreConfig) (ScoreStore, error) {
		if config.Postgres == nil {
			return nil, fmt.Errorf("postgres config is required for postgres store")
		}
		return NewPostgresStore(*config.Postgres)
	})

	// Register the Hybrid store
	Register(StoreTypeHybrid, func(config StoreConfig) (ScoreStore, error) {
		if config.Hybrid == nil {
			return nil, fmt.Errorf("hybrid config is required for hybrid store")
		}

		hc := config.Hybrid

		// Create primary store
		primary, err := NewStore(hc.Primary)
		if err != nil {
			return nil, fmt.Errorf("failed to create primary store: %w", err)
		}

		// Create cache store
		cache, err := NewStore(hc.Cache)
		if err != nil {
			primary.Close() // Clean up primary on failure
			return nil, fmt.Errorf("failed to create cache store: %w", err)
		}

		// Create hybrid store with ScoreStore instances
		return NewHybridStore(HybridConfig{
			Primary:           primary,
			Cache:             cache,
			CacheTTL:          hc.CacheTTL,
			WriteThrough:      hc.WriteThrough,
			AsyncWrite:        hc.AsyncWrite,
			InvalidateOnWrite: hc.InvalidateOnWrite,
		})
	})
}
