package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// Default key prefixes for Redis storage
	defaultRedisKeyPrefix = "chainkit:scoring:"
	scoreKeyPattern       = "score:"
	latencyKeyName        = "latency"
	lockKeyPattern        = "lock:"
	eventChannelName      = "events"
)

// RedisStore implements ScoreStore using Redis as the backend.
// It supports distributed deployments with pub/sub and distributed locking.
type RedisStore struct {
	client    *redis.Client
	config    RedisConfig
	keyPrefix string
}

// NewRedisStore creates a new Redis-based score store.
// It verifies the connection on creation and returns an error if Redis is unreachable.
func NewRedisStore(config RedisConfig) (*RedisStore, error) {
	// Set defaults
	if config.PoolSize == 0 {
		config.PoolSize = 10
	}
	if config.MinIdleConns == 0 {
		config.MinIdleConns = 2
	}

	// Create Redis client
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
		_ = client.Close()
		return nil, fmt.Errorf("redis connection failed: %w", err)
	}

	// Determine key prefix
	keyPrefix := defaultRedisKeyPrefix
	if config.KeyPrefix != "" {
		keyPrefix = config.KeyPrefix
		if keyPrefix[len(keyPrefix)-1] != ':' {
			keyPrefix += ":"
		}
	}

	return &RedisStore{
		client:    client,
		config:    config,
		keyPrefix: keyPrefix,
	}, nil
}

// Name returns the store type identifier.
func (r *RedisStore) Name() string {
	return "redis"
}

// scoreKey generates the Redis key for a provider's score.
func (r *RedisStore) scoreKey(providerName string) string {
	return r.keyPrefix + scoreKeyPattern + providerName
}

// latencyKey generates the Redis key for global latency statistics.
func (r *RedisStore) latencyKey() string {
	return r.keyPrefix + latencyKeyName
}

// lockKey generates the Redis key for a distributed lock.
func (r *RedisStore) lockKey(providerName string) string {
	return r.keyPrefix + lockKeyPattern + providerName
}

// eventChannel generates the Redis pub/sub channel name for score events.
func (r *RedisStore) eventChannel() string {
	return r.keyPrefix + eventChannelName
}

// GetScore retrieves the score data for a specific provider.
func (r *RedisStore) GetScore(ctx context.Context, providerName string) (*ProviderScoreData, error) {
	data, err := r.client.Get(ctx, r.scoreKey(providerName)).Bytes()
	if err == redis.Nil {
		return nil, nil // Not found, not an error
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

// SetScore stores or updates the score data for a provider.
func (r *RedisStore) SetScore(ctx context.Context, data *ProviderScoreData) error {
	if data == nil || data.Name == "" {
		return nil // Skip invalid data
	}

	bytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal score: %w", err)
	}

	key := r.scoreKey(data.Name)
	ttl := r.config.ScoreTTL

	if err := r.client.Set(ctx, key, bytes, ttl).Err(); err != nil {
		return fmt.Errorf("redis set: %w", err)
	}

	// Publish update event for watchers
	go r.publishScoreUpdate(context.Background(), data)

	return nil
}

// GetAllScores retrieves score data for all providers.
func (r *RedisStore) GetAllScores(ctx context.Context) ([]*ProviderScoreData, error) {
	var cursor uint64
	var scores []*ProviderScoreData
	pattern := r.keyPrefix + scoreKeyPattern + "*"

	// Use SCAN to iterate through all score keys
	for {
		keys, nextCursor, err := r.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("redis scan: %w", err)
		}

		if len(keys) > 0 {
			// Batch get all values
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
					// Skip invalid data, don't fail entire operation
					continue
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

// DeleteScore removes the score data for a specific provider.
func (r *RedisStore) DeleteScore(ctx context.Context, providerName string) error {
	if err := r.client.Del(ctx, r.scoreKey(providerName)).Err(); err != nil {
		return fmt.Errorf("redis del: %w", err)
	}
	return nil
}

// SetScores stores or updates multiple provider scores in a single operation.
// Uses Redis pipelining for efficiency.
func (r *RedisStore) SetScores(ctx context.Context, data []*ProviderScoreData) error {
	if len(data) == 0 {
		return nil
	}

	pipe := r.client.Pipeline()

	for _, score := range data {
		if score == nil || score.Name == "" {
			continue
		}

		bytes, err := json.Marshal(score)
		if err != nil {
			return fmt.Errorf("marshal score %s: %w", score.Name, err)
		}

		key := r.scoreKey(score.Name)
		ttl := r.config.ScoreTTL
		pipe.Set(ctx, key, bytes, ttl)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis pipeline exec: %w", err)
	}

	return nil
}

// GetLatencyStats retrieves the global latency statistics.
func (r *RedisStore) GetLatencyStats(ctx context.Context) (*LatencyStatsData, error) {
	data, err := r.client.Get(ctx, r.latencyKey()).Bytes()
	if err == redis.Nil {
		return nil, nil // Not found, not an error
	}
	if err != nil {
		return nil, fmt.Errorf("redis get latency: %w", err)
	}

	var stats LatencyStatsData
	if err := json.Unmarshal(data, &stats); err != nil {
		return nil, fmt.Errorf("unmarshal latency stats: %w", err)
	}

	return &stats, nil
}

// SetLatencyStats stores the global latency statistics.
func (r *RedisStore) SetLatencyStats(ctx context.Context, data *LatencyStatsData) error {
	if data == nil {
		return nil
	}

	bytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal latency stats: %w", err)
	}

	ttl := r.config.ScoreTTL // Use same TTL as scores
	if err := r.client.Set(ctx, r.latencyKey(), bytes, ttl).Err(); err != nil {
		return fmt.Errorf("redis set latency: %w", err)
	}

	return nil
}

// Close releases the Redis connection.
func (r *RedisStore) Close() error {
	return r.client.Close()
}

// Ping checks if the Redis server is accessible.
func (r *RedisStore) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// publishScoreUpdate publishes a score update event to Redis pub/sub.
// This is called asynchronously to avoid blocking the caller.
func (r *RedisStore) publishScoreUpdate(ctx context.Context, data *ProviderScoreData) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return // Silently fail, don't block on pub/sub errors
	}

	r.client.Publish(ctx, r.eventChannel(), bytes)
}

// Watchable interface implementation

// Watch subscribes to score changes and calls the callback function
// whenever a provider's score is updated.
// This uses Redis pub/sub and blocks until the context is canceled.
func (r *RedisStore) Watch(ctx context.Context, callback func(providerName string, data *ProviderScoreData)) error {
	pubsub := r.client.Subscribe(ctx, r.eventChannel())
	defer func() { _ = pubsub.Close() }()

	// Wait for subscription confirmation
	if _, err := pubsub.Receive(ctx); err != nil {
		return fmt.Errorf("subscribe failed: %w", err)
	}

	ch := pubsub.Channel()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return fmt.Errorf("pubsub channel closed")
			}

			var data ProviderScoreData
			if err := json.Unmarshal([]byte(msg.Payload), &data); err != nil {
				// Skip invalid messages, don't fail entire watch
				continue
			}

			callback(data.Name, &data)
		}
	}
}

// Expirable interface implementation

// SetScoreWithTTL stores a provider score with a specific expiration time.
// This overrides the default ScoreTTL for this specific score.
func (r *RedisStore) SetScoreWithTTL(ctx context.Context, data *ProviderScoreData, ttl time.Duration) error {
	if data == nil || data.Name == "" {
		return nil
	}

	bytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal score: %w", err)
	}

	key := r.scoreKey(data.Name)
	if err := r.client.Set(ctx, key, bytes, ttl).Err(); err != nil {
		return fmt.Errorf("redis set with ttl: %w", err)
	}

	// Publish update event
	go r.publishScoreUpdate(context.Background(), data)

	return nil
}

// Lockable interface implementation

// Lock acquires a distributed lock for a provider's score.
// The lock is automatically released when the unlock function is called
// or when the TTL expires.
// Returns an unlock function and an error.
// If the lock is already held, returns an error.
func (r *RedisStore) Lock(ctx context.Context, providerName string, ttl time.Duration) (unlock func(), err error) {
	lockKey := r.lockKey(providerName)
	lockValue := fmt.Sprintf("%d", time.Now().UnixNano())

	// Try to acquire lock using SET NX (set if not exists)
	// Use Set with Mode="NX" (SetNX is deprecated as of Redis 2.6.12)
	result, err := r.client.SetArgs(ctx, lockKey, lockValue, redis.SetArgs{
		Mode: "NX",
		TTL:  ttl,
	}).Result()
	if err == redis.Nil || result == "" {
		return nil, fmt.Errorf("lock already held for provider %s", providerName)
	}
	if err != nil {
		return nil, fmt.Errorf("redis set: %w", err)
	}

	// Return unlock function that only deletes if we still own the lock
	unlock = func() {
		// Use Lua script to ensure atomicity: only delete if value matches
		script := `
			if redis.call("get", KEYS[1]) == ARGV[1] then
				return redis.call("del", KEYS[1])
			else
				return 0
			end
		`
		// Use background context since the original context may be canceled
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		r.client.Eval(unlockCtx, script, []string{lockKey}, lockValue)
	}

	return unlock, nil
}

// Ensure RedisStore implements all interfaces
var (
	_ ScoreStore = (*RedisStore)(nil)
	_ Watchable  = (*RedisStore)(nil)
	_ Expirable  = (*RedisStore)(nil)
	_ Lockable   = (*RedisStore)(nil)
)
