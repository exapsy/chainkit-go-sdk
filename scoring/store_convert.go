package scoring

import (
	"time"

	"github.com/exapsy/chainkit/scoring/store"
)

// ToStoreData converts a ProviderScore to its serializable representation.
// This is used when persisting scores to a store.
func ToStoreData(ps *ProviderScore) *store.ProviderScoreData {
	if ps == nil {
		return nil
	}

	ps.mu.RLock()
	defer ps.mu.RUnlock()

	data := &store.ProviderScoreData{
		Name:             ps.Name,
		BaseScore:        ps.BaseScore,
		HealthPenalty:    ps.HealthPenalty,
		LatencyPenalty:   ps.LatencyPenalty,
		ErrorPenalty:     ps.ErrorPenalty,
		RateLimitPenalty: ps.RateLimitPenalty,
		TotalOperations:  ps.TotalOperations,
		SuccessfulOps:    ps.SuccessfulOps,
		FailedOps:        ps.FailedOps,
		LastUpdated:      ps.LastUpdated,
		LastHealthCheck:  ps.LastHealthCheck,
		LastOperation:    ps.LastOperation,
	}

	// Convert time.Duration latencies to int64 nanoseconds for serialization
	if len(ps.RecentLatencies) > 0 {
		data.RecentLatencies = make([]int64, len(ps.RecentLatencies))
		for i, latency := range ps.RecentLatencies {
			data.RecentLatencies[i] = latency.Nanoseconds()
		}
	}

	return data
}

// FromStoreData converts serialized score data back to a ProviderScore.
// This is used when loading scores from a store.
// The latencyWindowSize parameter sets the capacity for future latency samples.
// The historySize parameter sets the capacity of the in-memory penalty ring buffer.
func FromStoreData(data *store.ProviderScoreData, latencyWindowSize int, historySize int) *ProviderScore {
	if data == nil {
		return nil
	}

	ps := &ProviderScore{
		Name:              data.Name,
		BaseScore:         data.BaseScore,
		HealthPenalty:     data.HealthPenalty,
		LatencyPenalty:    data.LatencyPenalty,
		ErrorPenalty:      data.ErrorPenalty,
		RateLimitPenalty:  data.RateLimitPenalty,
		TotalOperations:   data.TotalOperations,
		SuccessfulOps:     data.SuccessfulOps,
		FailedOps:         data.FailedOps,
		LastUpdated:       data.LastUpdated,
		LastHealthCheck:   data.LastHealthCheck,
		LastOperation:     data.LastOperation,
		LatencyWindowSize: latencyWindowSize,
		history:           newPenaltyHistory(historySize),
	}

	// Convert int64 nanoseconds back to time.Duration
	if len(data.RecentLatencies) > 0 {
		ps.RecentLatencies = make([]time.Duration, 0, latencyWindowSize)
		for _, nanos := range data.RecentLatencies {
			ps.RecentLatencies = append(ps.RecentLatencies, time.Duration(nanos))
		}
	} else {
		ps.RecentLatencies = make([]time.Duration, 0, latencyWindowSize)
	}

	return ps
}

// LatencyTrackerToStoreData converts latency tracker data to its serializable representation.
func LatencyTrackerToStoreData(lt *LatencyTracker) *store.LatencyStatsData {
	if lt == nil {
		return nil
	}

	lt.mu.RLock()
	defer lt.mu.RUnlock()

	data := &store.LatencyStatsData{
		ProviderSamples: make(map[string][]int64, len(lt.samples)),
		LastUpdated:     time.Now(),
	}

	// Convert each provider's latency samples to nanoseconds
	for provider, samples := range lt.samples {
		if len(samples) > 0 {
			nanoSamples := make([]int64, len(samples))
			for i, duration := range samples {
				nanoSamples[i] = duration.Nanoseconds()
			}
			data.ProviderSamples[provider] = nanoSamples
		}
	}

	return data
}

// LatencyTrackerFromStoreData restores latency tracker data from its serializable representation.
func LatencyTrackerFromStoreData(data *store.LatencyStatsData, windowSize int) *LatencyTracker {
	if data == nil {
		return NewLatencyTracker(windowSize)
	}

	lt := NewLatencyTracker(windowSize)

	// Convert nanoseconds back to time.Duration for each provider
	for provider, nanoSamples := range data.ProviderSamples {
		if len(nanoSamples) > 0 {
			samples := make([]time.Duration, 0, windowSize)
			for _, nanos := range nanoSamples {
				samples = append(samples, time.Duration(nanos))
			}
			lt.samples[provider] = samples
		}
	}

	return lt
}

// AllScoresToStoreData converts all provider scores to their serializable representation.
// This is a convenience function for batch operations.
func AllScoresToStoreData(scores map[string]*ProviderScore) []*store.ProviderScoreData {
	if len(scores) == 0 {
		return nil
	}

	data := make([]*store.ProviderScoreData, 0, len(scores))
	for _, ps := range scores {
		if ps != nil {
			data = append(data, ToStoreData(ps))
		}
	}

	return data
}

// AllScoresFromStoreData converts serialized score data back to provider scores.
// This is a convenience function for batch operations.
func AllScoresFromStoreData(data []*store.ProviderScoreData, latencyWindowSize int, historySize int) map[string]*ProviderScore {
	if len(data) == 0 {
		return make(map[string]*ProviderScore)
	}

	scores := make(map[string]*ProviderScore, len(data))
	for _, d := range data {
		if d != nil && d.Name != "" {
			scores[d.Name] = FromStoreData(d, latencyWindowSize, historySize)
		}
	}

	return scores
}
