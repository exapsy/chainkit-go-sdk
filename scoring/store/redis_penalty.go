package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultRedisPenaltyKeyPrefix  = "chainkit:scoring:"
	penaltyKeyPattern             = "penalty:"
	defaultRedisPenaltyTTL        = 7 * 24 * time.Hour
	defaultRedisPenaltyMaxEntries = 500
)

// RedisPenaltyConfig holds configuration for the Redis-backed penalty history store.
type RedisPenaltyConfig struct {
	Addr     string
	Password string
	DB       int

	// KeyPrefix is prepended to all Redis keys. Default: "chainkit:scoring:"
	KeyPrefix string

	// TTL is the time-to-live applied to each provider's penalty list key.
	// Each Append call resets the TTL. Default: 7 days.
	TTL time.Duration

	// MaxEntriesPerProvider caps how many entries are kept per provider (LTRIM).
	// Default: 500.
	MaxEntriesPerProvider int

	PoolSize     int
	MinIdleConns int
}

// RedisPenaltyStore implements PenaltyHistoryStore using Redis Lists.
//
// Per-provider key: {prefix}penalty:{providerName}  (Redis List, newest at tail)
//
// PurgeOld is a no-op because the TTL handles expiry automatically.
type RedisPenaltyStore struct {
	client    *redis.Client
	config    RedisPenaltyConfig
	keyPrefix string
}

// NewRedisPenaltyStore creates a new Redis penalty history store and verifies connectivity.
func NewRedisPenaltyStore(config RedisPenaltyConfig) (*RedisPenaltyStore, error) {
	if config.PoolSize == 0 {
		config.PoolSize = 10
	}
	if config.MinIdleConns == 0 {
		config.MinIdleConns = 2
	}
	if config.TTL == 0 {
		config.TTL = defaultRedisPenaltyTTL
	}
	if config.MaxEntriesPerProvider == 0 {
		config.MaxEntriesPerProvider = defaultRedisPenaltyMaxEntries
	}

	client := redis.NewClient(&redis.Options{
		Addr:         config.Addr,
		Password:     config.Password,
		DB:           config.DB,
		PoolSize:     config.PoolSize,
		MinIdleConns: config.MinIdleConns,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis penalty store: connection failed: %w", err)
	}

	keyPrefix := defaultRedisPenaltyKeyPrefix
	if config.KeyPrefix != "" {
		keyPrefix = config.KeyPrefix
		if keyPrefix[len(keyPrefix)-1] != ':' {
			keyPrefix += ":"
		}
	}

	return &RedisPenaltyStore{
		client:    client,
		config:    config,
		keyPrefix: keyPrefix,
	}, nil
}

func (r *RedisPenaltyStore) Name() string { return "redis_penalty" }

func (r *RedisPenaltyStore) penaltyKey(providerName string) string {
	return r.keyPrefix + penaltyKeyPattern + providerName
}

// Append pushes a new penalty record to the tail of the provider's list,
// trims the list to MaxEntriesPerProvider, and resets the TTL.
func (r *RedisPenaltyStore) Append(ctx context.Context, record *PenaltyRecordData) error {
	if record == nil || record.ProviderName == "" {
		return nil
	}

	b, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("redis penalty store: marshal: %w", err)
	}

	key := r.penaltyKey(record.ProviderName)
	maxIdx := int64(r.config.MaxEntriesPerProvider - 1)

	pipe := r.client.Pipeline()
	pipe.RPush(ctx, key, b)
	pipe.LTrim(ctx, key, -int64(r.config.MaxEntriesPerProvider), -1)
	pipe.Expire(ctx, key, r.config.TTL)
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("redis penalty store: append pipeline: %w", err)
	}
	_ = maxIdx
	return nil
}

// GetRecent returns the most recent limit records for a provider (oldest first).
// Returns an empty slice when no records exist.
func (r *RedisPenaltyStore) GetRecent(ctx context.Context, providerName string, limit int) ([]*PenaltyRecordData, error) {
	if limit <= 0 {
		return []*PenaltyRecordData{}, nil
	}

	key := r.penaltyKey(providerName)
	// LRANGE -limit -1 returns the last `limit` items (oldest to newest in list order)
	vals, err := r.client.LRange(ctx, key, -int64(limit), -1).Result()
	if err == redis.Nil || len(vals) == 0 {
		return []*PenaltyRecordData{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis penalty store: lrange: %w", err)
	}

	out := make([]*PenaltyRecordData, 0, len(vals))
	for _, v := range vals {
		var rec PenaltyRecordData
		if err := json.Unmarshal([]byte(v), &rec); err != nil {
			continue // skip corrupted entries
		}
		out = append(out, &rec)
	}
	return out, nil
}

// PurgeOld is a no-op for Redis — the TTL set on each Append handles expiry.
func (r *RedisPenaltyStore) PurgeOld(_ context.Context, _ time.Duration) error {
	return nil
}

func (r *RedisPenaltyStore) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

func (r *RedisPenaltyStore) Close() error {
	return r.client.Close()
}
