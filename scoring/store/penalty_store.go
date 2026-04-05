package store

import (
	"context"
	"time"
)

// PenaltyRecordData is the serializable form of a penalty event.
type PenaltyRecordData struct {
	ProviderName string    `json:"provider_name"`
	Category     string    `json:"category"`
	Reason       string    `json:"reason"`
	Amount       float64   `json:"amount"`
	CreatedAt    time.Time `json:"created_at"`
}

// PenaltyHistoryStore persists penalty events per provider.
// All implementations must be safe for concurrent use.
//
// The store is append-only: records are never updated, only appended and eventually purged.
type PenaltyHistoryStore interface {
	// Append records a new penalty event.
	Append(ctx context.Context, record *PenaltyRecordData) error

	// GetRecent returns the most recent limit records for a provider,
	// ordered oldest-first (chronological order).
	// Returns an empty (non-nil) slice when no records exist.
	GetRecent(ctx context.Context, providerName string, limit int) ([]*PenaltyRecordData, error)

	// PurgeOld deletes all records older than the retention window.
	// Implementations that use TTL-based expiry (e.g. Redis) may treat this as a no-op.
	PurgeOld(ctx context.Context, retentionWindow time.Duration) error

	Close() error
	Ping(ctx context.Context) error
	Name() string
}
