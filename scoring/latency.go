package scoring

import (
	"math"
	"sync"
	"time"
)

// LatencyTracker tracks response times across all providers for relative comparison
type LatencyTracker struct {
	// samples stores recent latency samples per provider
	samples map[string][]time.Duration

	// windowSize is the maximum number of samples to keep per provider
	windowSize int

	// globalStats caches aggregate statistics across all providers
	globalStats *LatencyStats
	statsDirty  bool // indicates if stats need recalculation

	mu sync.RWMutex
}

// LatencyStats contains statistical information about latencies
type LatencyStats struct {
	Mean           time.Duration
	StdDev         time.Duration
	Min            time.Duration
	Max            time.Duration
	P50            time.Duration // Median
	P75            time.Duration
	P90            time.Duration
	P95            time.Duration
	P99            time.Duration
	SampleCount    int
	LastCalculated time.Time
}

// NewLatencyTracker creates a new latency tracker
func NewLatencyTracker(windowSize int) *LatencyTracker {
	if windowSize <= 0 {
		windowSize = 100 // Default window size
	}

	return &LatencyTracker{
		samples:     make(map[string][]time.Duration),
		windowSize:  windowSize,
		globalStats: nil,
		statsDirty:  true,
	}
}

// RecordLatency records a latency sample for a provider
func (lt *LatencyTracker) RecordLatency(provider string, latency time.Duration) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	// Initialize if first sample for this provider
	if _, exists := lt.samples[provider]; !exists {
		lt.samples[provider] = make([]time.Duration, 0, lt.windowSize)
	}

	// Add sample
	lt.samples[provider] = append(lt.samples[provider], latency)

	// Maintain sliding window
	if len(lt.samples[provider]) > lt.windowSize {
		lt.samples[provider] = lt.samples[provider][1:]
	}

	// Mark stats as dirty
	lt.statsDirty = true
}

// GetGlobalStats calculates and returns aggregate statistics across all providers
func (lt *LatencyTracker) GetGlobalStats() *LatencyStats {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	// Return cached stats if available and not dirty
	if !lt.statsDirty && lt.globalStats != nil {
		return lt.globalStats
	}

	// Collect all samples from all providers
	allSamples := make([]time.Duration, 0)
	for _, providerSamples := range lt.samples {
		allSamples = append(allSamples, providerSamples...)
	}

	if len(allSamples) == 0 {
		return &LatencyStats{
			LastCalculated: time.Now(),
		}
	}

	// Calculate statistics
	stats := calculateStats(allSamples)
	stats.LastCalculated = time.Now()

	// Cache the result
	lt.globalStats = stats
	lt.statsDirty = false

	return stats
}

// GetProviderSlownessFactor calculates how much slower a provider is compared to peers
// Returns value in standard deviations: 0 = average, >0 = slower, <0 = faster
func (lt *LatencyTracker) GetProviderSlownessFactor(provider string) float64 {
	// First, get provider samples under lock
	lt.mu.RLock()
	providerSamples, exists := lt.samples[provider]
	if !exists || len(providerSamples) == 0 {
		lt.mu.RUnlock()
		return 0 // No data, assume average
	}

	// Copy samples to avoid holding lock during calculations
	samplesCopy := make([]time.Duration, len(providerSamples))
	copy(samplesCopy, providerSamples)
	lt.mu.RUnlock()

	// Get global stats (this acquires its own lock)
	globalStats := lt.GetGlobalStats()

	if globalStats.StdDev == 0 {
		return 0 // No variation, all providers are similar
	}

	// Calculate provider's average latency from the copy
	var sum time.Duration
	for _, lat := range samplesCopy {
		sum += lat
	}
	providerAvg := sum / time.Duration(len(samplesCopy))

	// Calculate how many standard deviations away from mean
	difference := float64(providerAvg - globalStats.Mean)
	slownessFactor := difference / float64(globalStats.StdDev)

	return slownessFactor
}

// GetProviderStats returns latency statistics for a specific provider
func (lt *LatencyTracker) GetProviderStats(provider string) *LatencyStats {
	lt.mu.RLock()
	defer lt.mu.RUnlock()

	samples, exists := lt.samples[provider]
	if !exists || len(samples) == 0 {
		return &LatencyStats{}
	}

	// Make a copy to avoid holding lock during calculation
	samplesCopy := make([]time.Duration, len(samples))
	copy(samplesCopy, samples)

	return calculateStats(samplesCopy)
}

// calculateStats computes statistical measures from a slice of durations
func calculateStats(samples []time.Duration) *LatencyStats {
	if len(samples) == 0 {
		return &LatencyStats{}
	}

	// Sort samples for percentile calculations
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sortDurations(sorted)

	// Calculate mean
	var sum time.Duration
	for _, s := range samples {
		sum += s
	}
	mean := sum / time.Duration(len(samples))

	// Calculate standard deviation
	var variance float64
	for _, s := range samples {
		diff := float64(s - mean)
		variance += diff * diff
	}
	variance /= float64(len(samples))
	stdDev := time.Duration(math.Sqrt(variance))

	// Calculate percentiles
	stats := &LatencyStats{
		Mean:        mean,
		StdDev:      stdDev,
		Min:         sorted[0],
		Max:         sorted[len(sorted)-1],
		P50:         percentile(sorted, 50),
		P75:         percentile(sorted, 75),
		P90:         percentile(sorted, 90),
		P95:         percentile(sorted, 95),
		P99:         percentile(sorted, 99),
		SampleCount: len(samples),
	}

	return stats
}

// percentile calculates the nth percentile from sorted samples
func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}

	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}

	// Linear interpolation
	rank := float64(p) / 100.0 * float64(len(sorted)-1)
	lowerIndex := int(math.Floor(rank))
	upperIndex := int(math.Ceil(rank))

	if lowerIndex == upperIndex {
		return sorted[lowerIndex]
	}

	// Interpolate between lower and upper
	weight := rank - float64(lowerIndex)
	lower := float64(sorted[lowerIndex])
	upper := float64(sorted[upperIndex])
	interpolated := lower + weight*(upper-lower)

	return time.Duration(interpolated)
}

// sortDurations sorts a slice of durations in ascending order (simple bubble sort for small datasets)
func sortDurations(durations []time.Duration) {
	n := len(durations)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if durations[j] > durations[j+1] {
				durations[j], durations[j+1] = durations[j+1], durations[j]
			}
		}
	}
}

// Reset clears all latency data
func (lt *LatencyTracker) Reset() {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	lt.samples = make(map[string][]time.Duration)
	lt.globalStats = nil
	lt.statsDirty = true
}

// GetProviderCount returns the number of providers being tracked
func (lt *LatencyTracker) GetProviderCount() int {
	lt.mu.RLock()
	defer lt.mu.RUnlock()

	return len(lt.samples)
}
