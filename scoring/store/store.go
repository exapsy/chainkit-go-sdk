package store

import (
	"context"
	"time"
)

// ProviderScoreData represents the serializable score data for a provider.
// This is the data transfer object used for persistence and retrieval.
type ProviderScoreData struct {
	// Provider identification
	Name string `json:"name"`

	// Score components
	BaseScore        float64 `json:"base_score"`
	HealthPenalty    float64 `json:"health_penalty"`
	LatencyPenalty   float64 `json:"latency_penalty"`
	ErrorPenalty     float64 `json:"error_penalty"`
	RateLimitPenalty float64 `json:"rate_limit_penalty"`

	// Operation tracking
	TotalOperations int64 `json:"total_operations"`
	SuccessfulOps   int64 `json:"successful_ops"`
	FailedOps       int64 `json:"failed_ops"`

	// Timestamps
	LastUpdated     time.Time `json:"last_updated"`
	LastHealthCheck time.Time `json:"last_health_check,omitempty"`
	LastOperation   time.Time `json:"last_operation,omitempty"`

	// Latency samples (for full state restoration)
	// Stored as nanoseconds for consistent serialization
	RecentLatencies []int64 `json:"recent_latencies,omitempty"`
}

// LatencyStatsData represents global latency statistics across all providers.
type LatencyStatsData struct {
	// Map of provider name to latency samples (in nanoseconds)
	ProviderSamples map[string][]int64 `json:"provider_samples"`
	LastUpdated     time.Time          `json:"last_updated"`
}

// ScoreStore defines the interface for score persistence.
// Implementations can be in-memory, Redis, PostgreSQL, or hybrid.
type ScoreStore interface {
	// Provider score operations

	// GetScore retrieves the score data for a specific provider.
	// Returns nil, nil if the provider is not found (not an error).
	GetScore(ctx context.Context, providerName string) (*ProviderScoreData, error)

	// SetScore stores or updates the score data for a provider.
	SetScore(ctx context.Context, data *ProviderScoreData) error

	// GetAllScores retrieves score data for all providers.
	GetAllScores(ctx context.Context) ([]*ProviderScoreData, error)

	// DeleteScore removes the score data for a specific provider.
	DeleteScore(ctx context.Context, providerName string) error

	// Batch operations (for efficiency)

	// SetScores stores or updates multiple provider scores in a single operation.
	// This is more efficient than calling SetScore multiple times.
	SetScores(ctx context.Context, data []*ProviderScoreData) error

	// Latency data operations

	// GetLatencyStats retrieves the global latency statistics.
	// Returns nil, nil if no statistics are stored (not an error).
	GetLatencyStats(ctx context.Context) (*LatencyStatsData, error)

	// SetLatencyStats stores the global latency statistics.
	SetLatencyStats(ctx context.Context, data *LatencyStatsData) error

	// Lifecycle

	// Close releases any resources held by the store.
	Close() error

	// Ping checks if the store is accessible and healthy.
	Ping(ctx context.Context) error

	// Metadata

	// Name returns a human-readable name for this store type.
	Name() string
}

// Optional interfaces for extended functionality

// Watchable allows subscribing to score changes in real-time.
// This is useful for distributed systems where multiple instances
// need to stay synchronized.
type Watchable interface {
	ScoreStore

	// Watch subscribes to score changes and calls the callback function
	// whenever a provider's score is updated.
	// The watch continues until the context is canceled.
	Watch(ctx context.Context, callback func(providerName string, data *ProviderScoreData)) error
}

// Expirable allows setting TTL (time-to-live) on stored data.
// This is useful for automatic cleanup of stale data in distributed caches.
type Expirable interface {
	ScoreStore

	// SetScoreWithTTL stores a provider score with an expiration time.
	// The score will be automatically deleted after the TTL expires.
	SetScoreWithTTL(ctx context.Context, data *ProviderScoreData, ttl time.Duration) error
}

// Lockable provides distributed locking for score updates.
// This prevents race conditions when multiple instances update the same provider.
type Lockable interface {
	ScoreStore

	// Lock acquires a distributed lock for a provider's score.
	// The lock is automatically released when the unlock function is called
	// or when the TTL expires.
	// Returns an unlock function and an error.
	Lock(ctx context.Context, providerName string, ttl time.Duration) (unlock func(), err error)
}
