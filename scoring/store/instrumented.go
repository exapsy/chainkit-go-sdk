package store

import (
	"context"
	"time"

	"github.com/exapsy/chainkit/scoring/metrics"
)

// InstrumentedStore wraps any ScoreStore and records timing and outcome
// metrics for every operation via a metrics.Recorder.
//
// Use this to add observability to any store implementation without
// modifying the store itself.
//
// Example:
//
//	base := store.NewRedisStore(config)
//	instrumented := store.NewInstrumentedStore(base, metricsRecorder)
type InstrumentedStore struct {
	inner    ScoreStore
	recorder metrics.Recorder
}

// NewInstrumentedStore wraps inner with metrics instrumentation.
// If recorder is nil, a no-op recorder is used.
func NewInstrumentedStore(inner ScoreStore, recorder metrics.Recorder) *InstrumentedStore {
	if recorder == nil {
		recorder = &metrics.NoOpRecorder{}
	}
	return &InstrumentedStore{inner: inner, recorder: recorder}
}

// Name returns the name of the underlying store, prefixed with "instrumented/".
func (s *InstrumentedStore) Name() string {
	return "instrumented/" + s.inner.Name()
}

// GetScore retrieves a provider's score data, recording the operation duration.
func (s *InstrumentedStore) GetScore(ctx context.Context, providerName string) (*ProviderScoreData, error) {
	start := time.Now()
	data, err := s.inner.GetScore(ctx, providerName)
	s.recorder.RecordStoreOperation(ctx, s.inner.Name(), "get_score", time.Since(start), err)
	return data, err
}

// SetScore stores a provider's score data, recording the operation duration.
func (s *InstrumentedStore) SetScore(ctx context.Context, data *ProviderScoreData) error {
	start := time.Now()
	err := s.inner.SetScore(ctx, data)
	s.recorder.RecordStoreOperation(ctx, s.inner.Name(), "set_score", time.Since(start), err)
	return err
}

// GetAllScores retrieves all provider scores, recording the operation duration.
func (s *InstrumentedStore) GetAllScores(ctx context.Context) ([]*ProviderScoreData, error) {
	start := time.Now()
	data, err := s.inner.GetAllScores(ctx)
	s.recorder.RecordStoreOperation(ctx, s.inner.Name(), "get_all_scores", time.Since(start), err)
	return data, err
}

// DeleteScore deletes a provider's score data, recording the operation duration.
func (s *InstrumentedStore) DeleteScore(ctx context.Context, providerName string) error {
	start := time.Now()
	err := s.inner.DeleteScore(ctx, providerName)
	s.recorder.RecordStoreOperation(ctx, s.inner.Name(), "delete_score", time.Since(start), err)
	return err
}

// SetScores stores multiple provider scores, recording the operation duration.
func (s *InstrumentedStore) SetScores(ctx context.Context, data []*ProviderScoreData) error {
	start := time.Now()
	err := s.inner.SetScores(ctx, data)
	s.recorder.RecordStoreOperation(ctx, s.inner.Name(), "set_scores", time.Since(start), err)
	return err
}

// GetLatencyStats retrieves latency statistics, recording the operation duration.
func (s *InstrumentedStore) GetLatencyStats(ctx context.Context) (*LatencyStatsData, error) {
	start := time.Now()
	data, err := s.inner.GetLatencyStats(ctx)
	s.recorder.RecordStoreOperation(ctx, s.inner.Name(), "get_latency_stats", time.Since(start), err)
	return data, err
}

// SetLatencyStats stores latency statistics, recording the operation duration.
func (s *InstrumentedStore) SetLatencyStats(ctx context.Context, data *LatencyStatsData) error {
	start := time.Now()
	err := s.inner.SetLatencyStats(ctx, data)
	s.recorder.RecordStoreOperation(ctx, s.inner.Name(), "set_latency_stats", time.Since(start), err)
	return err
}

// Close closes the underlying store.
func (s *InstrumentedStore) Close() error {
	return s.inner.Close()
}

// Ping checks the health of the underlying store.
func (s *InstrumentedStore) Ping(ctx context.Context) error {
	return s.inner.Ping(ctx)
}

// Ensure InstrumentedStore implements ScoreStore at compile time.
var _ ScoreStore = (*InstrumentedStore)(nil)
