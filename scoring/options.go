package scoring

import (
	"time"

	"github.com/exapsy/chainkit/scoring/metrics"
	"github.com/exapsy/chainkit/scoring/store"
)

// ScoringConfig holds all configuration for the scoring engine
type ScoringConfig struct {
	// Penalty weights (how much each event type affects the score)

	// HealthCheckFailPenalty is applied when a health check fails (non-429, non-auth)
	HealthCheckFailPenalty float64

	// RateLimitPenalty is applied when a provider returns 429 or rate limit errors
	RateLimitPenalty float64

	// AuthFailurePenalty is applied for authentication/authorization failures (401/403)
	AuthFailurePenalty float64

	// SlowResponsePenalty is applied per standard deviation of slowness relative to peers
	SlowResponsePenalty float64

	// OperationFailPenalty is applied when a provider operation fails
	OperationFailPenalty float64

	// TimeoutPenalty is applied when an operation times out
	TimeoutPenalty float64

	// SuccessBonus is subtracted from error penalty on successful operations (gradual recovery)
	SuccessBonus float64

	// Decay settings (gradual recovery of scores over time)

	// DecayInterval defines how often decay is applied to penalties
	DecayInterval time.Duration

	// DecayRate defines the percentage of penalty reduction per decay interval (0.0-1.0)
	// e.g., 0.1 means 10% reduction every decay interval
	DecayRate float64

	// MaxPenalty defines the maximum total penalty that can be accumulated per category
	// This prevents a provider from being permanently disabled
	MaxPenalty float64

	// Latency comparison settings

	// LatencyWindowSize defines how many recent latency samples to keep per provider
	LatencyWindowSize int

	// SlowThresholdStdDev defines how many standard deviations above mean is considered "slow"
	// Penalties are only applied if latency exceeds this threshold
	SlowThresholdStdDev float64

	// MinSamplesForLatencyComparison defines minimum samples needed before applying latency penalties
	MinSamplesForLatencyComparison int

	// Enabled controls whether adaptive scoring is active
	Enabled bool

	// Store is the optional persistent storage backend
	Store store.ScoreStore

	// MetricsRecorder is the metrics backend.
	// Defaults to a no-op recorder if not set.
	// Setting this via WithMetrics automatically instruments the store too.
	MetricsRecorder metrics.Recorder
}

// DefaultScoringConfig returns a sensible default configuration
func DefaultScoringConfig() ScoringConfig {
	return ScoringConfig{
		// Penalty weights
		HealthCheckFailPenalty: 5.0,  // Moderate penalty for health check failures
		RateLimitPenalty:       20.0, // High penalty for rate limiting (serious issue)
		AuthFailurePenalty:     50.0, // Very high penalty for auth failures (config issue)
		SlowResponsePenalty:    2.0,  // 2 points per standard deviation of slowness
		OperationFailPenalty:   3.0,  // Moderate penalty for operation failures
		TimeoutPenalty:         10.0, // High penalty for timeouts
		SuccessBonus:           0.5,  // Small bonus per successful operation
		MaxPenalty:             90.0, // Can't lose more than 90 points (keeps min 10% of base score)

		// Decay settings
		DecayInterval: 1 * time.Minute, // Apply decay every minute
		DecayRate:     0.1,             // 10% penalty reduction per minute

		// Latency settings
		LatencyWindowSize:              100, // Keep 100 recent samples
		SlowThresholdStdDev:            1.0, // Penalize if >1 std dev above mean
		MinSamplesForLatencyComparison: 10,  // Need at least 10 samples before comparing

		// Enabled by default when engine is created
		Enabled: true,
	}
}

// ScoringOption is a functional option for configuring the scoring engine
type ScoringOption func(*ScoringConfig)

// WithHealthCheckPenalty sets the penalty for health check failures
func WithHealthCheckPenalty(penalty float64) ScoringOption {
	return func(c *ScoringConfig) {
		c.HealthCheckFailPenalty = penalty
	}
}

// WithRateLimitPenalty sets the penalty for rate limit errors (429)
func WithRateLimitPenalty(penalty float64) ScoringOption {
	return func(c *ScoringConfig) {
		c.RateLimitPenalty = penalty
	}
}

// WithAuthFailurePenalty sets the penalty for authentication failures (401/403)
func WithAuthFailurePenalty(penalty float64) ScoringOption {
	return func(c *ScoringConfig) {
		c.AuthFailurePenalty = penalty
	}
}

// WithSlowResponsePenalty sets the penalty per standard deviation of slowness
func WithSlowResponsePenalty(penalty float64) ScoringOption {
	return func(c *ScoringConfig) {
		c.SlowResponsePenalty = penalty
	}
}

// WithOperationFailPenalty sets the penalty for operation failures
func WithOperationFailPenalty(penalty float64) ScoringOption {
	return func(c *ScoringConfig) {
		c.OperationFailPenalty = penalty
	}
}

// WithTimeoutPenalty sets the penalty for operation timeouts
func WithTimeoutPenalty(penalty float64) ScoringOption {
	return func(c *ScoringConfig) {
		c.TimeoutPenalty = penalty
	}
}

// WithSuccessBonus sets the bonus for successful operations
func WithSuccessBonus(bonus float64) ScoringOption {
	return func(c *ScoringConfig) {
		c.SuccessBonus = bonus
	}
}

// WithMaxPenalty sets the maximum total penalty per category
func WithMaxPenalty(maxPenalty float64) ScoringOption {
	return func(c *ScoringConfig) {
		c.MaxPenalty = maxPenalty
	}
}

// WithDecayInterval sets how often decay is applied to penalties
func WithDecayInterval(interval time.Duration) ScoringOption {
	return func(c *ScoringConfig) {
		c.DecayInterval = interval
	}
}

// WithDecayRate sets the percentage of penalty reduction per decay interval
func WithDecayRate(rate float64) ScoringOption {
	return func(c *ScoringConfig) {
		if rate < 0 {
			rate = 0
		}
		if rate > 1 {
			rate = 1
		}
		c.DecayRate = rate
	}
}

// WithLatencyWindow sets the number of latency samples to keep per provider
func WithLatencyWindow(size int) ScoringOption {
	return func(c *ScoringConfig) {
		if size < 10 {
			size = 10 // Minimum window size
		}
		c.LatencyWindowSize = size
	}
}

// WithSlowThreshold sets the standard deviation threshold for "slow" classification
func WithSlowThreshold(stdDevs float64) ScoringOption {
	return func(c *ScoringConfig) {
		c.SlowThresholdStdDev = stdDevs
	}
}

// WithMinLatencySamples sets the minimum samples needed before latency comparison
func WithMinLatencySamples(count int) ScoringOption {
	return func(c *ScoringConfig) {
		c.MinSamplesForLatencyComparison = count
	}
}

// WithEnabled enables or disables the scoring engine
func WithEnabled(enabled bool) ScoringOption {
	return func(c *ScoringConfig) {
		c.Enabled = enabled
	}
}

// WithStore sets a custom score store for persistence.
// If not set, the engine uses in-memory storage only (no persistence).
func WithStore(s store.ScoreStore) ScoringOption {
	return func(c *ScoringConfig) {
		c.Store = s
	}
}

// WithMemoryStore configures the engine to use in-memory storage (default).
// This is useful for explicitly setting the default behavior.
func WithMemoryStore() ScoringOption {
	return func(c *ScoringConfig) {
		c.Store = store.NewMemoryStore()
	}
}

// WithRedisStore configures the engine to use Redis-based storage.
// Returns an error-wrapping option that falls back to memory store on failure.
//
// Example:
//
//	engine := NewEngine(
//	    WithRedisStore("localhost:6379", RedisPassword("secret"), RedisDB(1)),
//	)
func WithRedisStore(addr string, opts ...RedisStoreOption) ScoringOption {
	return func(c *ScoringConfig) {
		config := store.RedisConfig{
			Addr:         addr,
			PoolSize:     10,
			MinIdleConns: 2,
		}

		// Apply Redis-specific options
		for _, opt := range opts {
			opt(&config)
		}

		s, err := store.NewRedisStore(config)
		if err != nil {
			// Fall back to memory store on error
			c.Store = store.NewMemoryStore()
		} else {
			c.Store = s
		}
	}
}

// RedisStoreOption is a functional option for configuring Redis store.
type RedisStoreOption func(*store.RedisConfig)

// RedisPassword sets the Redis password for authentication.
func RedisPassword(password string) RedisStoreOption {
	return func(c *store.RedisConfig) {
		c.Password = password
	}
}

// RedisDB sets the Redis database number (0-15).
func RedisDB(db int) RedisStoreOption {
	return func(c *store.RedisConfig) {
		c.DB = db
	}
}

// RedisScoreTTL sets the time-to-live for score data in Redis.
// A value of 0 means no expiration (default).
func RedisScoreTTL(ttl time.Duration) RedisStoreOption {
	return func(c *store.RedisConfig) {
		c.ScoreTTL = ttl
	}
}

// RedisKeyPrefix sets a custom key prefix for Redis keys.
// Useful for multi-tenant deployments or namespacing.
func RedisKeyPrefix(prefix string) RedisStoreOption {
	return func(c *store.RedisConfig) {
		c.KeyPrefix = prefix
	}
}

// RedisPoolSize sets the maximum number of socket connections.
func RedisPoolSize(size int) RedisStoreOption {
	return func(c *store.RedisConfig) {
		c.PoolSize = size
	}
}

// RedisMinIdleConns sets the minimum number of idle connections.
func RedisMinIdleConns(conns int) RedisStoreOption {
	return func(c *store.RedisConfig) {
		c.MinIdleConns = conns
	}
}

// WithPostgresStore configures the engine to use PostgreSQL-based storage.
// Returns an error-wrapping option that falls back to memory store on failure.
//
// Example:
//
//	engine := NewEngine(
//	    WithPostgresStore(
//	        "postgres://user:pass@localhost/chainkit",
//	        PostgresTablePrefix("scoring_"),
//	        PostgresMaxConns(25),
//	    ),
//	)
func WithPostgresStore(connectionString string, opts ...PostgresStoreOption) ScoringOption {
	return func(c *ScoringConfig) {
		config := store.PostgresConfig{
			ConnectionString: connectionString,
			TablePrefix:      "chainkit_",
			MaxOpenConns:     25,
			MaxIdleConns:     5,
			ConnMaxLifetime:  5 * time.Minute,
		}

		// Apply Postgres-specific options
		for _, opt := range opts {
			opt(&config)
		}

		s, err := store.NewPostgresStore(config)
		if err != nil {
			// Fall back to memory store on error
			c.Store = store.NewMemoryStore()
		} else {
			c.Store = s
		}
	}
}

// PostgresStoreOption is a functional option for configuring PostgreSQL store.
type PostgresStoreOption func(*store.PostgresConfig)

// PostgresTablePrefix sets a custom table prefix for PostgreSQL tables.
// Default is "chainkit_" which creates tables like "chainkit_provider_scores".
func PostgresTablePrefix(prefix string) PostgresStoreOption {
	return func(c *store.PostgresConfig) {
		c.TablePrefix = prefix
	}
}

// PostgresMaxConns sets the maximum number of open connections to the database.
// Default is 25.
func PostgresMaxConns(max int) PostgresStoreOption {
	return func(c *store.PostgresConfig) {
		c.MaxOpenConns = max
	}
}

// PostgresMaxIdleConns sets the maximum number of idle connections.
// Default is 5.
func PostgresMaxIdleConns(max int) PostgresStoreOption {
	return func(c *store.PostgresConfig) {
		c.MaxIdleConns = max
	}
}

// PostgresConnMaxLifetime sets the maximum lifetime of a connection.
// Default is 5 minutes.
func PostgresConnMaxLifetime(duration time.Duration) PostgresStoreOption {
	return func(c *store.PostgresConfig) {
		c.ConnMaxLifetime = duration
	}
}

// WithHybridStore configures the engine to use a hybrid store (cache + primary).
// Typically combines Redis (cache) with PostgreSQL (primary) for optimal performance.
//
// Example:
//
//	engine := NewEngine(
//	    WithHybridStore(
//	        "postgres://user:pass@localhost/chainkit",
//	        "localhost:6379",
//	        HybridCacheTTL(5*time.Minute),
//	        HybridWriteThrough(true),
//	    ),
//	)
func WithHybridStore(primaryConnString, cacheAddr string, opts ...HybridStoreOption) ScoringOption {
	return func(c *ScoringConfig) {
		config := store.HybridStoreConfig{
			Primary: store.StoreConfig{
				Type: store.StoreTypePostgres,
				Postgres: &store.PostgresConfig{
					ConnectionString: primaryConnString,
					TablePrefix:      "chainkit_",
					MaxOpenConns:     25,
					MaxIdleConns:     5,
					ConnMaxLifetime:  5 * time.Minute,
				},
			},
			Cache: store.StoreConfig{
				Type: store.StoreTypeRedis,
				Redis: &store.RedisConfig{
					Addr:         cacheAddr,
					PoolSize:     10,
					MinIdleConns: 2,
				},
			},
			CacheTTL:     5 * time.Minute,
			WriteThrough: true,
			AsyncWrite:   false,
		}

		// Apply hybrid-specific options
		for _, opt := range opts {
			opt(&config)
		}

		s, err := store.NewStore(store.StoreConfig{
			Type:   store.StoreTypeHybrid,
			Hybrid: &config,
		})
		if err != nil {
			// Fall back to memory store on error
			c.Store = store.NewMemoryStore()
		} else {
			c.Store = s
		}
	}
}

// HybridStoreOption is a functional option for configuring hybrid store.
type HybridStoreOption func(*store.HybridStoreConfig)

// HybridCacheTTL sets how long cache entries are considered fresh.
// Default is 5 minutes.
func HybridCacheTTL(ttl time.Duration) HybridStoreOption {
	return func(c *store.HybridStoreConfig) {
		c.CacheTTL = ttl
	}
}

// HybridWriteThrough controls whether writes go to both stores.
// If true (default), writes are sent to both primary and cache.
// If false, writes only go to primary (cache populated on read miss).
func HybridWriteThrough(enabled bool) HybridStoreOption {
	return func(c *store.HybridStoreConfig) {
		c.WriteThrough = enabled
	}
}

// HybridAsyncWrite controls whether cache writes happen asynchronously.
// Only applies when WriteThrough is true. Default is false (synchronous).
func HybridAsyncWrite(enabled bool) HybridStoreOption {
	return func(c *store.HybridStoreConfig) {
		c.AsyncWrite = enabled
	}
}

// HybridInvalidateOnWrite controls whether cache entries are invalidated
// on writes instead of being updated. Useful for write-heavy workloads.
// Default is false (cache is updated on write).
func HybridInvalidateOnWrite(enabled bool) HybridStoreOption {
	return func(c *store.HybridStoreConfig) {
		c.InvalidateOnWrite = enabled
	}
}

// HybridPrimaryConfig allows customizing the primary store configuration.
func HybridPrimaryConfig(fn func(*store.StoreConfig)) HybridStoreOption {
	return func(c *store.HybridStoreConfig) {
		fn(&c.Primary)
	}
}

// HybridCacheConfig allows customizing the cache store configuration.
func HybridCacheConfig(fn func(*store.StoreConfig)) HybridStoreOption {
	return func(c *store.HybridStoreConfig) {
		fn(&c.Cache)
	}
}

// WithMetrics sets the metrics recorder for the scoring engine.
//
// When a recorder is set, the engine automatically wraps the configured
// store with instrumentation — so a single WithMetrics call instruments
// both the engine (score changes, events, latency, rankings) and all
// store operations (timing, error rates, cache hit rates).
//
// Example — Prometheus:
//
//	import promrecorder "github.com/exapsy/chainkit/scoring/metrics/prometheus"
//
//	engine := scoring.NewEngine(
//	    scoring.WithRedisStore("localhost:6379"),
//	    scoring.WithMetrics(promrecorder.NewRecorder(promrecorder.DefaultConfig())),
//	)
//
// Example — OpenTelemetry:
//
//	import otelrecorder "github.com/exapsy/chainkit/scoring/metrics/otel"
//
//	rec, _ := otelrecorder.NewRecorder(otelrecorder.DefaultConfig())
//	engine := scoring.NewEngine(
//	    scoring.WithMetrics(rec),
//	)
//
// Example — custom recorder:
//
//	engine := scoring.NewEngine(
//	    scoring.WithMetrics(&myRecorder{}),
//	)
func WithMetrics(recorder metrics.Recorder) ScoringOption {
	return func(c *ScoringConfig) {
		c.MetricsRecorder = recorder
	}
}

// WithStoreConfig creates a store from configuration and sets it.
// This is a convenience function for common store setups.
func WithStoreConfig(config store.StoreConfig) ScoringOption {
	return func(c *ScoringConfig) {
		s, err := store.NewStore(config)
		if err != nil {
			// If store creation fails, fall back to memory store
			// In a real implementation, you might want to handle this differently
			c.Store = store.NewMemoryStore()
		} else {
			c.Store = s
		}
	}
}

// Validate checks if the configuration is valid
func (c *ScoringConfig) Validate() error {
	if c.DecayRate < 0 || c.DecayRate > 1 {
		c.DecayRate = 0.1 // Reset to default
	}

	if c.LatencyWindowSize < 10 {
		c.LatencyWindowSize = 10 // Minimum
	}

	if c.DecayInterval < 0 {
		c.DecayInterval = 1 * time.Minute // Default
	}

	return nil
}
