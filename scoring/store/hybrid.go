package store

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/exapsy/chainkit/scoring/metrics"
)

// HybridConfig configures the hybrid store behavior.
type HybridConfig struct {
	// Primary store (source of truth, typically PostgreSQL)
	Primary ScoreStore

	// Cache store (fast reads, typically Redis)
	Cache ScoreStore

	// CacheTTL determines how long cache entries are considered fresh
	// before checking the primary store. Default: 5 minutes
	CacheTTL time.Duration

	// WriteThrough controls whether writes go to both stores synchronously.
	// If true, writes go to both primary and cache.
	// If false, writes only go to primary (cache populated on read miss).
	// Default: true
	WriteThrough bool

	// AsyncWrite controls whether cache writes happen asynchronously.
	// Only applies when WriteThrough is true.
	// Default: false (synchronous)
	AsyncWrite bool

	// InvalidateOnWrite controls whether cache entries are invalidated
	// on writes instead of being updated. Useful for write-heavy workloads.
	// Default: false
	InvalidateOnWrite bool
}

// HybridStore combines a primary store with a cache store for optimal
// performance and durability.
//
// Typical usage: PostgreSQL (primary) + Redis (cache)
//
// Read strategy:
//  1. Check cache if fresh (within CacheTTL)
//  2. On cache miss, read from primary
//  3. Populate cache with primary data
//
// Write strategy (WriteThrough=true):
//  1. Write to primary (blocking)
//  2. Write to cache (sync or async based on AsyncWrite)
//
// Write strategy (WriteThrough=false):
//  1. Write to primary only
//  2. Invalidate cache entry
//  3. Next read will repopulate cache
type HybridStore struct {
	primary  ScoreStore
	cache    ScoreStore
	config   HybridConfig
	recorder metrics.Recorder // nil = no cache-hit recording

	// Track cache freshness per provider
	cacheTime map[string]time.Time
	cacheMu   sync.RWMutex
}

// NewHybridStore creates a new hybrid store.
func NewHybridStore(config HybridConfig) (*HybridStore, error) {
	if config.Primary == nil {
		return nil, fmt.Errorf("primary store is required")
	}
	if config.Cache == nil {
		return nil, fmt.Errorf("cache store is required")
	}

	// Set defaults
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

// Name returns the store type identifier.
func (h *HybridStore) Name() string {
	return fmt.Sprintf("hybrid(%s+%s)", h.primary.Name(), h.cache.Name())
}

// isCacheFresh checks if the cache entry for a provider is still fresh.
func (h *HybridStore) isCacheFresh(name string) bool {
	h.cacheMu.RLock()
	defer h.cacheMu.RUnlock()

	if t, ok := h.cacheTime[name]; ok {
		return time.Since(t) < h.config.CacheTTL
	}
	return false
}

// markCacheFresh marks a cache entry as fresh with the current timestamp.
func (h *HybridStore) markCacheFresh(name string) {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	h.cacheTime[name] = time.Now()
}

// invalidateCache removes cache freshness tracking for a provider.
func (h *HybridStore) invalidateCache(name string) {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	delete(h.cacheTime, name)
}

// SetRecorder configures a metrics recorder for cache hit/miss tracking.
// This is called automatically by the engine when WithMetrics is used.
func (h *HybridStore) SetRecorder(r metrics.Recorder) {
	h.recorder = r
}

// GetScore retrieves a provider's score, trying cache first if fresh.
func (h *HybridStore) GetScore(ctx context.Context, name string) (*ProviderScoreData, error) {
	// Try cache first if fresh
	if h.isCacheFresh(name) {
		data, err := h.cache.GetScore(ctx, name)
		if err == nil && data != nil {
			if h.recorder != nil {
				h.recorder.RecordCacheHit(ctx, h.Name(), true)
			}
			return data, nil
		}
		// Cache miss or error, fall through to primary
	}

	if h.recorder != nil {
		h.recorder.RecordCacheHit(ctx, h.Name(), false)
	}

	// Read from primary (source of truth)
	data, err := h.primary.GetScore(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("primary store get failed: %w", err)
	}

	// Populate cache on miss
	if data != nil {
		if h.config.AsyncWrite {
			go func() {
				ctx := context.Background()
				_ = h.cache.SetScore(ctx, data)
			}()
		} else {
			// Ignore cache write errors - cache is optional
			_ = h.cache.SetScore(ctx, data)
		}
		h.markCacheFresh(name)
	}

	return data, nil
}

// SetScore stores a provider's score in the primary and optionally the cache.
func (h *HybridStore) SetScore(ctx context.Context, data *ProviderScoreData) error {
	if data == nil {
		return fmt.Errorf("data cannot be nil")
	}

	// Always write to primary first (source of truth)
	if err := h.primary.SetScore(ctx, data); err != nil {
		return fmt.Errorf("primary store set failed: %w", err)
	}

	// Handle cache updates
	if h.config.InvalidateOnWrite {
		// Invalidate cache entry instead of updating
		h.invalidateCache(data.Name)
		_ = h.cache.DeleteScore(ctx, data.Name)
	} else if h.config.WriteThrough {
		// Write to cache
		if h.config.AsyncWrite {
			go func() {
				ctx := context.Background()
				_ = h.cache.SetScore(ctx, data)
			}()
		} else {
			// Ignore cache write errors
			_ = h.cache.SetScore(ctx, data)
		}
		h.markCacheFresh(data.Name)
	} else {
		// Write-behind mode: invalidate cache, will repopulate on next read
		h.invalidateCache(data.Name)
	}

	return nil
}

// SetScores stores multiple provider scores in batch.
func (h *HybridStore) SetScores(ctx context.Context, data []*ProviderScoreData) error {
	if len(data) == 0 {
		return nil
	}

	// Always write to primary first
	if err := h.primary.SetScores(ctx, data); err != nil {
		return fmt.Errorf("primary store batch set failed: %w", err)
	}

	// Handle cache updates
	if h.config.InvalidateOnWrite {
		// Invalidate all cache entries
		for _, d := range data {
			h.invalidateCache(d.Name)
			_ = h.cache.DeleteScore(ctx, d.Name)
		}
	} else if h.config.WriteThrough {
		// Write to cache
		if h.config.AsyncWrite {
			go func() {
				ctx := context.Background()
				_ = h.cache.SetScores(ctx, data)
			}()
		} else {
			_ = h.cache.SetScores(ctx, data)
		}
		for _, d := range data {
			h.markCacheFresh(d.Name)
		}
	} else {
		// Write-behind mode: invalidate cache entries
		for _, d := range data {
			h.invalidateCache(d.Name)
		}
	}

	return nil
}

// GetAllScores retrieves all provider scores from the primary store.
// Always reads from primary to ensure consistency.
func (h *HybridStore) GetAllScores(ctx context.Context) ([]*ProviderScoreData, error) {
	data, err := h.primary.GetAllScores(ctx)
	if err != nil {
		return nil, fmt.Errorf("primary store get all failed: %w", err)
	}
	return data, nil
}

// DeleteScore removes a provider's score from both stores.
func (h *HybridStore) DeleteScore(ctx context.Context, name string) error {
	// Delete from primary
	if err := h.primary.DeleteScore(ctx, name); err != nil {
		return fmt.Errorf("primary store delete failed: %w", err)
	}

	// Delete from cache (ignore errors)
	_ = h.cache.DeleteScore(ctx, name)

	// Remove from cache tracking
	h.invalidateCache(name)

	return nil
}

// GetLatencyStats retrieves global latency statistics from the primary store.
func (h *HybridStore) GetLatencyStats(ctx context.Context) (*LatencyStatsData, error) {
	data, err := h.primary.GetLatencyStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("primary store get latency stats failed: %w", err)
	}
	return data, nil
}

// SetLatencyStats stores global latency statistics in the primary store.
func (h *HybridStore) SetLatencyStats(ctx context.Context, data *LatencyStatsData) error {
	if err := h.primary.SetLatencyStats(ctx, data); err != nil {
		return fmt.Errorf("primary store set latency stats failed: %w", err)
	}
	return nil
}

// Close closes both the cache and primary stores.
func (h *HybridStore) Close() error {
	var errs []error

	// Close cache first
	if err := h.cache.Close(); err != nil {
		errs = append(errs, fmt.Errorf("cache close: %w", err))
	}

	// Close primary
	if err := h.primary.Close(); err != nil {
		errs = append(errs, fmt.Errorf("primary close: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}

	return nil
}

// Ping checks connectivity to both stores.
func (h *HybridStore) Ping(ctx context.Context) error {
	// Check primary first (critical)
	if err := h.primary.Ping(ctx); err != nil {
		return fmt.Errorf("primary store ping failed: %w", err)
	}

	// Check cache (non-critical, but report error)
	if err := h.cache.Ping(ctx); err != nil {
		return fmt.Errorf("cache store ping failed: %w", err)
	}

	return nil
}

// InvalidateAll clears all cache freshness tracking.
// This forces the next GetScore calls to read from primary.
func (h *HybridStore) InvalidateAll() {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	h.cacheTime = make(map[string]time.Time)
}

// WarmCache populates the cache with all scores from the primary store.
func (h *HybridStore) WarmCache(ctx context.Context) error {
	// Get all scores from primary
	scores, err := h.primary.GetAllScores(ctx)
	if err != nil {
		return fmt.Errorf("failed to get scores from primary: %w", err)
	}

	// Populate cache
	if len(scores) > 0 {
		if err := h.cache.SetScores(ctx, scores); err != nil {
			return fmt.Errorf("failed to populate cache: %w", err)
		}

		// Mark all as fresh
		for _, score := range scores {
			h.markCacheFresh(score.Name)
		}
	}

	return nil
}

// GetPrimary returns the primary store (for advanced use cases).
func (h *HybridStore) GetPrimary() ScoreStore {
	return h.primary
}

// GetCache returns the cache store (for advanced use cases).
func (h *HybridStore) GetCache() ScoreStore {
	return h.cache
}

// GetConfig returns the hybrid configuration.
func (h *HybridStore) GetConfig() HybridConfig {
	return h.config
}
