package scoring

import (
	"sync"
	"time"
)

// ProviderScore represents the dynamic score for a single provider
type ProviderScore struct {
	// Name of the provider
	Name string

	// BaseScore derived from initial priority (inverted: priority 1 → score 100, priority 2 → 90, etc.)
	BaseScore float64

	// Penalty components (these reduce the effective score)
	HealthPenalty    float64 // Accumulated from health check failures
	LatencyPenalty   float64 // Penalty for being slower than peers
	ErrorPenalty     float64 // Accumulated from operation failures
	RateLimitPenalty float64 // Accumulated from 429s and rate limiting

	// Tracking metadata
	LastUpdated     time.Time
	LastHealthCheck time.Time
	LastOperation   time.Time
	TotalOperations int64
	SuccessfulOps   int64
	FailedOps       int64

	// Latency tracking (for relative comparison)
	RecentLatencies   []time.Duration
	LatencyWindowSize int

	// history holds recent penalty events for diagnostics.
	// Guarded by mu — never nil after NewProviderScore.
	history *penaltyHistory

	mu sync.RWMutex
}

// NewProviderScore creates a new provider score from an initial priority
// Priority is inverted: priority 1 gets highest base score (100)
func NewProviderScore(name string, priority int, latencyWindowSize int, historySize int) *ProviderScore {
	// Convert priority to base score: priority 1 = 100, 2 = 90, 3 = 80, etc.
	// Ensures priority 1 always starts ahead, but adaptive scoring can override
	baseScore := 110.0 - (float64(priority) * 10.0)
	if baseScore < 10.0 {
		baseScore = 10.0 // Minimum base score
	}

	return &ProviderScore{
		Name:              name,
		BaseScore:         baseScore,
		HealthPenalty:     0,
		LatencyPenalty:    0,
		ErrorPenalty:      0,
		RateLimitPenalty:  0,
		LastUpdated:       time.Now(),
		RecentLatencies:   make([]time.Duration, 0, latencyWindowSize),
		LatencyWindowSize: latencyWindowSize,
		TotalOperations:   0,
		SuccessfulOps:     0,
		FailedOps:         0,
		history:           newPenaltyHistory(historySize),
	}
}

// EffectiveScore calculates the current effective score
// Higher score = better provider
func (ps *ProviderScore) EffectiveScore() float64 {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	score := ps.BaseScore - ps.HealthPenalty - ps.LatencyPenalty -
		ps.ErrorPenalty - ps.RateLimitPenalty

	// Ensure score doesn't go below 0
	if score < 0 {
		return 0
	}

	return score
}

// AddHealthPenalty adds a penalty for health check failures.
// reason is a human-readable explanation recorded in the penalty history.
func (ps *ProviderScore) AddHealthPenalty(penalty float64, maxPenalty float64, reason string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.HealthPenalty += penalty
	if ps.HealthPenalty > maxPenalty {
		ps.HealthPenalty = maxPenalty
	}
	now := time.Now()
	ps.LastHealthCheck = now
	ps.LastUpdated = now
	ps.history.add(PenaltyRecord{
		Timestamp: now,
		Category:  PenaltyCategoryHealth,
		Reason:    reason,
		Amount:    penalty,
	})
}

// AddRateLimitPenalty adds a penalty for rate limiting.
// reason is a human-readable explanation recorded in the penalty history.
func (ps *ProviderScore) AddRateLimitPenalty(penalty float64, maxPenalty float64, reason string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.RateLimitPenalty += penalty
	if ps.RateLimitPenalty > maxPenalty {
		ps.RateLimitPenalty = maxPenalty
	}
	now := time.Now()
	ps.LastUpdated = now
	ps.history.add(PenaltyRecord{
		Timestamp: now,
		Category:  PenaltyCategoryRateLimit,
		Reason:    reason,
		Amount:    penalty,
	})
}

// AddErrorPenalty adds a penalty for operation failures.
// reason is a human-readable explanation recorded in the penalty history.
func (ps *ProviderScore) AddErrorPenalty(penalty float64, maxPenalty float64, reason string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.ErrorPenalty += penalty
	if ps.ErrorPenalty > maxPenalty {
		ps.ErrorPenalty = maxPenalty
	}
	ps.FailedOps++
	ps.TotalOperations++
	now := time.Now()
	ps.LastOperation = now
	ps.LastUpdated = now
	ps.history.add(PenaltyRecord{
		Timestamp: now,
		Category:  PenaltyCategoryError,
		Reason:    reason,
		Amount:    penalty,
	})
}

// RecordSuccess records a successful operation and may reduce penalties
func (ps *ProviderScore) RecordSuccess(successBonus float64) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// Small bonus for successful operations
	if successBonus > 0 {
		ps.ErrorPenalty -= successBonus
		if ps.ErrorPenalty < 0 {
			ps.ErrorPenalty = 0
		}
	}

	ps.SuccessfulOps++
	ps.TotalOperations++
	ps.LastOperation = time.Now()
	ps.LastUpdated = time.Now()
}

// RecordLatency records a latency sample for relative comparison
func (ps *ProviderScore) RecordLatency(latency time.Duration) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.RecentLatencies = append(ps.RecentLatencies, latency)

	// Keep only the most recent samples (sliding window)
	if len(ps.RecentLatencies) > ps.LatencyWindowSize {
		ps.RecentLatencies = ps.RecentLatencies[1:]
	}
}

// GetAverageLatency calculates the average latency from recent samples
func (ps *ProviderScore) GetAverageLatency() time.Duration {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	if len(ps.RecentLatencies) == 0 {
		return 0
	}

	var sum time.Duration
	for _, lat := range ps.RecentLatencies {
		sum += lat
	}

	return sum / time.Duration(len(ps.RecentLatencies))
}

// UpdateLatencyPenalty updates the latency penalty based on comparison to peers.
// slownessFactor represents how much slower this provider is compared to peers (0 = average, >0 = slower).
// reason is a human-readable explanation; a penalty history entry is only recorded when penalty > 0.
func (ps *ProviderScore) UpdateLatencyPenalty(slownessFactor float64, penaltyPerStdDev float64, reason string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// Calculate penalty: slownessFactor is in standard deviations
	// Positive slowness = penalty, negative slowness = bonus (faster than average)
	penalty := slownessFactor * penaltyPerStdDev

	// Update penalty, but don't let it go negative (no bonus for speed, just less penalty)
	ps.LatencyPenalty = penalty
	if ps.LatencyPenalty < 0 {
		ps.LatencyPenalty = 0
	}

	now := time.Now()
	ps.LastUpdated = now

	if penalty > 0 && reason != "" {
		ps.history.add(PenaltyRecord{
			Timestamp: now,
			Category:  PenaltyCategoryLatency,
			Reason:    reason,
			Amount:    penalty,
		})
	}
}

// ApplyDecay reduces all penalties by a decay rate (for gradual recovery)
// decayRate should be between 0 and 1 (e.g., 0.1 = 10% reduction)
func (ps *ProviderScore) ApplyDecay(decayRate float64) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.HealthPenalty *= (1.0 - decayRate)
	ps.RateLimitPenalty *= (1.0 - decayRate)
	ps.ErrorPenalty *= (1.0 - decayRate)

	// Latency penalty doesn't decay (it's recalculated based on current performance)

	// Clean up very small penalties
	if ps.HealthPenalty < 0.01 {
		ps.HealthPenalty = 0
	}
	if ps.RateLimitPenalty < 0.01 {
		ps.RateLimitPenalty = 0
	}
	if ps.ErrorPenalty < 0.01 {
		ps.ErrorPenalty = 0
	}

	ps.LastUpdated = time.Now()
}

// GetStats returns a snapshot of the score statistics
func (ps *ProviderScore) GetStats() ProviderScoreStats {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	successRate := 0.0
	if ps.TotalOperations > 0 {
		successRate = float64(ps.SuccessfulOps) / float64(ps.TotalOperations)
	}

	return ProviderScoreStats{
		Name:             ps.Name,
		BaseScore:        ps.BaseScore,
		EffectiveScore:   ps.BaseScore - ps.HealthPenalty - ps.LatencyPenalty - ps.ErrorPenalty - ps.RateLimitPenalty,
		HealthPenalty:    ps.HealthPenalty,
		LatencyPenalty:   ps.LatencyPenalty,
		ErrorPenalty:     ps.ErrorPenalty,
		RateLimitPenalty: ps.RateLimitPenalty,
		TotalOperations:  ps.TotalOperations,
		SuccessfulOps:    ps.SuccessfulOps,
		FailedOps:        ps.FailedOps,
		SuccessRate:      successRate,
		AverageLatency:   ps.getAverageLatencyUnsafe(),
		LastUpdated:      ps.LastUpdated,
		RecentPenalties:  ps.history.snapshot(),
	}
}

// getAverageLatencyUnsafe calculates average latency without locking (internal use)
func (ps *ProviderScore) getAverageLatencyUnsafe() time.Duration {
	if len(ps.RecentLatencies) == 0 {
		return 0
	}

	var sum time.Duration
	for _, lat := range ps.RecentLatencies {
		sum += lat
	}

	return sum / time.Duration(len(ps.RecentLatencies))
}

// ProviderScoreStats represents a snapshot of provider score statistics
type ProviderScoreStats struct {
	Name             string
	BaseScore        float64
	EffectiveScore   float64
	HealthPenalty    float64
	LatencyPenalty   float64
	ErrorPenalty     float64
	RateLimitPenalty float64
	TotalOperations  int64
	SuccessfulOps    int64
	FailedOps        int64
	SuccessRate      float64
	AverageLatency   time.Duration
	LastUpdated      time.Time
	RecentPenalties  []PenaltyRecord
}
