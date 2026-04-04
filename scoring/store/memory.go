package store

import (
	"context"
	"sync"
)

// MemoryStore is an in-memory implementation of ScoreStore.
// It stores all data in memory and loses state on restart.
// This is the default store and suitable for single-instance deployments.
type MemoryStore struct {
	scores  map[string]*ProviderScoreData
	latency *LatencyStatsData
	mu      sync.RWMutex
}

// NewMemoryStore creates a new in-memory score store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		scores: make(map[string]*ProviderScoreData),
	}
}

// Name returns the store type identifier.
func (m *MemoryStore) Name() string {
	return "memory"
}

// GetScore retrieves the score data for a specific provider.
func (m *MemoryStore) GetScore(ctx context.Context, providerName string) (*ProviderScoreData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if data, ok := m.scores[providerName]; ok {
		// Return a copy to prevent external modifications
		return m.copyScoreData(data), nil
	}
	return nil, nil // Not found, not an error
}

// SetScore stores or updates the score data for a provider.
func (m *MemoryStore) SetScore(ctx context.Context, data *ProviderScoreData) error {
	if data == nil || data.Name == "" {
		return nil // Skip invalid data
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Store a copy to prevent external modifications
	m.scores[data.Name] = m.copyScoreData(data)
	return nil
}

// GetAllScores retrieves score data for all providers.
func (m *MemoryStore) GetAllScores(ctx context.Context) ([]*ProviderScoreData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	scores := make([]*ProviderScoreData, 0, len(m.scores))
	for _, data := range m.scores {
		scores = append(scores, m.copyScoreData(data))
	}
	return scores, nil
}

// DeleteScore removes the score data for a specific provider.
func (m *MemoryStore) DeleteScore(ctx context.Context, providerName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.scores, providerName)
	return nil
}

// SetScores stores or updates multiple provider scores in a single operation.
func (m *MemoryStore) SetScores(ctx context.Context, data []*ProviderScoreData) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, score := range data {
		if score != nil && score.Name != "" {
			m.scores[score.Name] = m.copyScoreData(score)
		}
	}
	return nil
}

// GetLatencyStats retrieves the global latency statistics.
func (m *MemoryStore) GetLatencyStats(ctx context.Context) (*LatencyStatsData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.latency == nil {
		return nil, nil // Not found, not an error
	}

	// Return a copy
	return m.copyLatencyData(m.latency), nil
}

// SetLatencyStats stores the global latency statistics.
func (m *MemoryStore) SetLatencyStats(ctx context.Context, data *LatencyStatsData) error {
	if data == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Store a copy
	m.latency = m.copyLatencyData(data)
	return nil
}

// Close releases any resources held by the store.
// For MemoryStore, this is a no-op.
func (m *MemoryStore) Close() error {
	return nil
}

// Ping checks if the store is accessible and healthy.
// For MemoryStore, this always succeeds.
func (m *MemoryStore) Ping(ctx context.Context) error {
	return nil
}

// Helper methods for deep copying data

func (m *MemoryStore) copyScoreData(src *ProviderScoreData) *ProviderScoreData {
	if src == nil {
		return nil
	}

	dst := &ProviderScoreData{
		Name:             src.Name,
		BaseScore:        src.BaseScore,
		HealthPenalty:    src.HealthPenalty,
		LatencyPenalty:   src.LatencyPenalty,
		ErrorPenalty:     src.ErrorPenalty,
		RateLimitPenalty: src.RateLimitPenalty,
		TotalOperations:  src.TotalOperations,
		SuccessfulOps:    src.SuccessfulOps,
		FailedOps:        src.FailedOps,
		LastUpdated:      src.LastUpdated,
		LastHealthCheck:  src.LastHealthCheck,
		LastOperation:    src.LastOperation,
	}

	// Copy latency samples slice
	if len(src.RecentLatencies) > 0 {
		dst.RecentLatencies = make([]int64, len(src.RecentLatencies))
		copy(dst.RecentLatencies, src.RecentLatencies)
	}

	return dst
}

func (m *MemoryStore) copyLatencyData(src *LatencyStatsData) *LatencyStatsData {
	if src == nil {
		return nil
	}

	dst := &LatencyStatsData{
		LastUpdated:     src.LastUpdated,
		ProviderSamples: make(map[string][]int64, len(src.ProviderSamples)),
	}

	// Deep copy the map and slices
	for provider, samples := range src.ProviderSamples {
		if len(samples) > 0 {
			copiedSamples := make([]int64, len(samples))
			copy(copiedSamples, samples)
			dst.ProviderSamples[provider] = copiedSamples
		} else {
			dst.ProviderSamples[provider] = nil
		}
	}

	return dst
}
