package store

import (
	"context"
	"time"
)

// CompositePenaltyStore writes to both a primary store (Postgres) and a cache store (Redis).
//
// Reads use the cache store first; if the cache returns an empty result the primary is tried.
// Writes are sent to the primary synchronously and to the cache asynchronously (fire-and-forget).
// PurgeOld only runs on the primary — the cache handles expiry via its own TTL mechanism.
type CompositePenaltyStore struct {
	primary PenaltyHistoryStore
	cache   PenaltyHistoryStore
}

// NewCompositePenaltyStore creates a composite store from a primary and cache store.
// Both arguments must be non-nil.
func NewCompositePenaltyStore(primary, cache PenaltyHistoryStore) *CompositePenaltyStore {
	return &CompositePenaltyStore{primary: primary, cache: cache}
}

func (c *CompositePenaltyStore) Name() string {
	return "composite_penalty(" + c.primary.Name() + "+" + c.cache.Name() + ")"
}

// Append records the penalty event in the primary store synchronously,
// then writes to the cache in a background goroutine.
func (c *CompositePenaltyStore) Append(ctx context.Context, record *PenaltyRecordData) error {
	if err := c.primary.Append(ctx, record); err != nil {
		return err
	}
	go func() {
		_ = c.cache.Append(context.Background(), record)
	}()
	return nil
}

// GetRecent returns the most recent limit records (oldest first).
// The cache is tried first; on error or empty result the primary is queried.
func (c *CompositePenaltyStore) GetRecent(ctx context.Context, providerName string, limit int) ([]*PenaltyRecordData, error) {
	records, err := c.cache.GetRecent(ctx, providerName, limit)
	if err == nil && len(records) > 0 {
		return records, nil
	}
	return c.primary.GetRecent(ctx, providerName, limit)
}

// PurgeOld deletes old records from the primary store.
// The cache relies on its TTL for expiry and does not need explicit purging.
func (c *CompositePenaltyStore) PurgeOld(ctx context.Context, retentionWindow time.Duration) error {
	return c.primary.PurgeOld(ctx, retentionWindow)
}

func (c *CompositePenaltyStore) Ping(ctx context.Context) error {
	if err := c.primary.Ping(ctx); err != nil {
		return err
	}
	return c.cache.Ping(ctx)
}

func (c *CompositePenaltyStore) Close() error {
	primaryErr := c.primary.Close()
	cacheErr := c.cache.Close()
	if primaryErr != nil {
		return primaryErr
	}
	return cacheErr
}
