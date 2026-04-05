package scoring

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/exapsy/chainkit/scoring/metrics"
	"github.com/exapsy/chainkit/scoring/store"
)

// Engine is the main scoring engine that orchestrates provider scoring
type Engine struct {
	config              ScoringConfig
	scores              map[string]*ProviderScore
	latencyTracker      *LatencyTracker
	decayManager        *DecayManager
	store               store.ScoreStore          // Optional persistent storage
	penaltyHistoryStore store.PenaltyHistoryStore // Optional; nil = in-memory only
	metrics             metrics.Recorder          // Always non-nil (NoOpRecorder by default)
	cleanupCancel       context.CancelFunc        // cancels the penalty cleanup goroutine
	mu                  sync.RWMutex
	started             bool
}

// NewEngine creates a new scoring engine with the given options
func NewEngine(opts ...ScoringOption) *Engine {
	config := DefaultScoringConfig()

	// Apply options
	for _, opt := range opts {
		opt(&config)
	}

	// Validate configuration
	_ = config.Validate()

	// Resolve metrics recorder — always non-nil so call sites need no nil checks.
	rec := config.MetricsRecorder
	if rec == nil {
		rec = &metrics.NoOpRecorder{}
	}

	// If both a store and a metrics recorder are configured, automatically
	// wrap the store so that store operations are instrumented too.
	// Users get full observability from a single WithMetrics() call.
	s := config.Store
	if s != nil && config.MetricsRecorder != nil {
		// For HybridStore, also wire the recorder so cache hit/miss events
		// are reported from inside the store's read path.
		if hs, ok := s.(*store.HybridStore); ok {
			hs.SetRecorder(rec)
		}
		s = store.NewInstrumentedStore(s, rec)
	}

	scores := make(map[string]*ProviderScore)
	latencyTracker := NewLatencyTracker(config.LatencyWindowSize)
	decayManager := NewDecayManager(config, scores)

	e := &Engine{
		config:              config,
		scores:              scores,
		latencyTracker:      latencyTracker,
		decayManager:        decayManager,
		store:               s,
		penaltyHistoryStore: config.PenaltyHistoryStore,
		metrics:             rec,
		started:             false,
	}

	// Load scores from store if available
	if e.store != nil {
		_ = e.LoadFromStore(context.Background())
	}

	return e
}

// Start begins background processes (decay manager + penalty history cleanup)
func (e *Engine) Start(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.started {
		return
	}

	e.started = true
	e.decayManager.Start(ctx)

	// Warm in-memory ring buffers from the persistent store.
	e.warmPenaltyHistory(ctx)

	// Start background cleanup for the persistent penalty store.
	if e.penaltyHistoryStore != nil && e.config.PenaltyCleanupInterval > 0 {
		cleanupCtx, cancel := context.WithCancel(ctx)
		e.cleanupCancel = cancel
		go func() {
			ticker := time.NewTicker(e.config.PenaltyCleanupInterval)
			defer ticker.Stop()
			for {
				select {
				case <-cleanupCtx.Done():
					return
				case <-ticker.C:
					_ = e.penaltyHistoryStore.PurgeOld(context.Background(), e.config.RetentionWindow)
				}
			}
		}()
	}
}

// Stop halts background processes
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.started {
		return
	}

	e.started = false
	e.decayManager.Stop()

	if e.cleanupCancel != nil {
		e.cleanupCancel()
		e.cleanupCancel = nil
	}
}

// RegisterProvider registers a new provider with the scoring engine
// priority is the static priority (1 = highest), used to calculate base score
func (e *Engine) RegisterProvider(name string, priority int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.scores[name]; exists {
		return // Already registered
	}

	score := NewProviderScore(name, priority, e.config.LatencyWindowSize, e.config.PenaltyHistorySize)
	e.scores[name] = score

	// Update decay manager's score map
	e.decayManager.UpdateScores(e.scores)
}

// UnregisterProvider removes a provider from scoring
func (e *Engine) UnregisterProvider(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	delete(e.scores, name)
	e.decayManager.UpdateScores(e.scores)
}

// RecordEvent processes a scoring event and updates the provider's score
func (e *Engine) RecordEvent(event ScoreEvent) {
	if !e.config.Enabled {
		return
	}

	e.mu.RLock()
	score, exists := e.scores[event.Provider]
	e.mu.RUnlock()

	if !exists {
		// Provider not registered, ignore
		return
	}

	ctx := context.Background()

	// Record latency if available
	if event.ResponseTime > 0 {
		e.latencyTracker.RecordLatency(event.Provider, event.ResponseTime)
		score.RecordLatency(event.ResponseTime)
		e.metrics.RecordLatency(ctx, event.Provider, string(event.Type), event.ResponseTime)

		// Update latency penalty based on comparison to peers
		e.updateLatencyPenalty(event.Provider)
	}

	// Apply penalties based on event type
	success := event.Error == nil
	ts := event.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	switch event.Type {
	case EventHealthCheckFailed:
		const reason = "health check failed"
		score.AddHealthPenalty(e.config.HealthCheckFailPenalty, e.config.MaxPenalty, reason)
		e.persistPenalty(event.Provider, PenaltyCategoryHealth, reason, e.config.HealthCheckFailPenalty, ts)

	case EventHealthCheck429:
		const reason = "rate limit during health check (HTTP 429)"
		score.AddRateLimitPenalty(e.config.RateLimitPenalty, e.config.MaxPenalty, reason)
		e.persistPenalty(event.Provider, PenaltyCategoryRateLimit, reason, e.config.RateLimitPenalty, ts)

	case EventHealthCheckAuthFail:
		const reason = "authentication failure (HTTP 401/403)"
		score.AddHealthPenalty(e.config.AuthFailurePenalty, e.config.MaxPenalty, reason)
		e.persistPenalty(event.Provider, PenaltyCategoryHealth, reason, e.config.AuthFailurePenalty, ts)

	case EventHealthCheckTimeout:
		const reason = "health check timed out"
		score.AddHealthPenalty(e.config.TimeoutPenalty, e.config.MaxPenalty, reason)
		e.persistPenalty(event.Provider, PenaltyCategoryHealth, reason, e.config.TimeoutPenalty, ts)

	case EventOperationFailed:
		reason := "operation failed"
		if event.Error != nil {
			reason = event.Error.Error()
		}
		score.AddErrorPenalty(e.config.OperationFailPenalty, e.config.MaxPenalty, reason)
		e.persistPenalty(event.Provider, PenaltyCategoryError, reason, e.config.OperationFailPenalty, ts)

	case EventRateLimited:
		const reason = "rate limited (HTTP 429)"
		score.AddRateLimitPenalty(e.config.RateLimitPenalty, e.config.MaxPenalty, reason)
		e.persistPenalty(event.Provider, PenaltyCategoryRateLimit, reason, e.config.RateLimitPenalty, ts)

	case EventOperationSuccess:
		score.RecordSuccess(e.config.SuccessBonus)

	case EventSlowResponse:
		// Latency penalty is handled by updateLatencyPenalty above.
	}

	e.metrics.RecordEvent(ctx, event.Provider, string(event.Type), success)
	e.metrics.RecordEffectiveScore(ctx, event.Provider, score.EffectiveScore())
}

// updateLatencyPenalty calculates and updates the latency penalty for a provider
func (e *Engine) updateLatencyPenalty(providerName string) {
	// Get global statistics
	globalStats := e.latencyTracker.GetGlobalStats()

	// Need minimum samples before comparing
	if globalStats.SampleCount < e.config.MinSamplesForLatencyComparison {
		return
	}

	// Get per-provider stats before acquiring the score lock
	providerStats := e.latencyTracker.GetProviderStats(providerName)

	// Get this provider's slowness factor (in standard deviations)
	slownessFactor := e.latencyTracker.GetProviderSlownessFactor(providerName)

	// Only apply penalty if significantly slower than threshold
	var reason string
	if slownessFactor < e.config.SlowThresholdStdDev {
		slownessFactor = 0 // Not slow enough to penalize
	} else {
		// Subtract threshold so penalty starts from 0 at threshold
		slownessFactor = slownessFactor - e.config.SlowThresholdStdDev

		// Build a diagnostic reason with P95 context
		ratio := 0.0
		if globalStats.P95 > 0 && providerStats != nil {
			ratio = float64(providerStats.P95) / float64(globalStats.P95)
		}
		globalP95ms := int64(0)
		providerP95ms := int64(0)
		if globalStats.P95 > 0 {
			globalP95ms = globalStats.P95.Milliseconds()
		}
		if providerStats != nil && providerStats.P95 > 0 {
			providerP95ms = providerStats.P95.Milliseconds()
		}
		reason = fmt.Sprintf("%.1fx slower than peers (provider P95: %dms, global P95: %dms)",
			ratio, providerP95ms, globalP95ms)
	}

	e.mu.RLock()
	score, exists := e.scores[providerName]
	e.mu.RUnlock()

	if !exists || score == nil {
		return
	}

	score.UpdateLatencyPenalty(slownessFactor, e.config.SlowResponsePenalty, reason)

	// Persist the latency penalty event if one was applied
	if slownessFactor > 0 && reason != "" {
		penalty := slownessFactor * e.config.SlowResponsePenalty
		e.persistPenalty(providerName, PenaltyCategoryLatency, reason, penalty, time.Now())
	}
}

// persistPenalty appends a penalty record to the persistent store in a background goroutine.
// It is a no-op when no PenaltyHistoryStore is configured.
func (e *Engine) persistPenalty(provider string, cat PenaltyCategory, reason string, amount float64, ts time.Time) {
	if e.penaltyHistoryStore == nil {
		return
	}
	record := &store.PenaltyRecordData{
		ProviderName: provider,
		Category:     string(cat),
		Reason:       reason,
		Amount:       amount,
		CreatedAt:    ts,
	}
	go func() {
		_ = e.penaltyHistoryStore.Append(context.Background(), record)
	}()
}

// warmPenaltyHistory populates the in-memory ring buffers from the persistent store.
// Must be called with e.mu held (write lock) or before e is exposed to other goroutines.
func (e *Engine) warmPenaltyHistory(ctx context.Context) {
	if e.penaltyHistoryStore == nil {
		return
	}

	for name, score := range e.scores {
		if score == nil {
			continue
		}
		records, err := e.penaltyHistoryStore.GetRecent(ctx, name, e.config.PenaltyHistorySize)
		if err != nil || len(records) == 0 {
			continue
		}
		score.mu.Lock()
		for _, r := range records {
			score.history.add(PenaltyRecord{
				Timestamp: r.CreatedAt,
				Category:  PenaltyCategory(r.Category),
				Reason:    r.Reason,
				Amount:    r.Amount,
			})
		}
		score.mu.Unlock()
	}
}

// GetEffectiveScore returns the current effective score for a provider
// Higher score = better provider
func (e *Engine) GetEffectiveScore(providerName string) float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()

	score, exists := e.scores[providerName]
	if !exists {
		return 0
	}

	return score.EffectiveScore()
}

// GetProviderStats returns detailed statistics for a provider
func (e *Engine) GetProviderStats(providerName string) *ProviderScoreStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	score, exists := e.scores[providerName]
	if !exists {
		return nil
	}

	stats := score.GetStats()
	return &stats
}

// GetAllProviderStats returns statistics for all registered providers
func (e *Engine) GetAllProviderStats() []ProviderScoreStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	stats := make([]ProviderScoreStats, 0, len(e.scores))
	for _, score := range e.scores {
		if score != nil {
			stats = append(stats, score.GetStats())
		}
	}

	return stats
}

// GetSortedProviders returns provider names sorted by effective score (highest first)
func (e *Engine) GetSortedProviders() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Collect all providers with their scores
	type providerScore struct {
		name  string
		score float64
	}

	providers := make([]providerScore, 0, len(e.scores))
	for name, score := range e.scores {
		if score != nil {
			providers = append(providers, providerScore{
				name:  name,
				score: score.EffectiveScore(),
			})
		}
	}

	// Sort by score (descending)
	for i := 0; i < len(providers)-1; i++ {
		for j := i + 1; j < len(providers); j++ {
			if providers[i].score < providers[j].score {
				providers[i], providers[j] = providers[j], providers[i]
			}
		}
	}

	// Extract names and record ranks
	names := make([]string, len(providers))
	ctx := context.Background()
	for i, p := range providers {
		names[i] = p.name
		e.metrics.RecordProviderRank(ctx, p.name, i+1, len(providers))
	}

	return names
}

// GetLatencyStats returns global latency statistics across all providers
func (e *Engine) GetLatencyStats() *LatencyStats {
	return e.latencyTracker.GetGlobalStats()
}

// GetProviderLatencyStats returns latency statistics for a specific provider
func (e *Engine) GetProviderLatencyStats(providerName string) *LatencyStats {
	return e.latencyTracker.GetProviderStats(providerName)
}

// Reset clears all scores and statistics (useful for testing)
func (e *Engine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Reset all scores to base values
	for _, score := range e.scores {
		if score != nil {
			score.mu.Lock()
			score.HealthPenalty = 0
			score.LatencyPenalty = 0
			score.ErrorPenalty = 0
			score.RateLimitPenalty = 0
			score.TotalOperations = 0
			score.SuccessfulOps = 0
			score.FailedOps = 0
			score.RecentLatencies = make([]time.Duration, 0, score.LatencyWindowSize)
			score.LastUpdated = time.Now()
			score.history = newPenaltyHistory(cap(score.history.buf))
			score.mu.Unlock()
		}
	}

	// Reset latency tracker
	e.latencyTracker.Reset()
}

// UpdateConfig updates the engine configuration
func (e *Engine) UpdateConfig(opts ...ScoringOption) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Apply options to current config
	for _, opt := range opts {
		opt(&e.config)
	}

	// Validate
	if err := e.config.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Update decay manager
	e.decayManager.UpdateConfig(e.config)

	return nil
}

// GetConfig returns a copy of the current configuration
func (e *Engine) GetConfig() ScoringConfig {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return e.config
}

// IsEnabled returns whether adaptive scoring is enabled
func (e *Engine) IsEnabled() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return e.config.Enabled
}

// SetEnabled enables or disables adaptive scoring
func (e *Engine) SetEnabled(enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.config.Enabled = enabled
}

// GetProviderCount returns the number of registered providers
func (e *Engine) GetProviderCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return len(e.scores)
}

// HasProvider checks if a provider is registered
func (e *Engine) HasProvider(name string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	_, exists := e.scores[name]
	return exists
}

// SetStore sets the persistent store for the engine.
// If a store is already set, it will be replaced.
// This will attempt to load scores from the new store.
func (e *Engine) SetStore(s store.ScoreStore) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.store = s

	// Load scores from the new store
	if s != nil {
		return e.loadFromStoreUnsafe(context.Background())
	}

	return nil
}

// GetStore returns the current store (may be nil).
func (e *Engine) GetStore() store.ScoreStore {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return e.store
}

// LoadFromStore loads all scores from the persistent store.
// This overwrites any existing in-memory scores.
func (e *Engine) LoadFromStore(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.loadFromStoreUnsafe(ctx)
}

// loadFromStoreUnsafe loads scores without acquiring the lock (internal use).
func (e *Engine) loadFromStoreUnsafe(ctx context.Context) error {
	if e.store == nil {
		return nil // No store configured, nothing to load
	}

	// Load all provider scores
	data, err := e.store.GetAllScores(ctx)
	if err != nil {
		return fmt.Errorf("failed to load scores from store: %w", err)
	}

	// Convert store data to provider scores
	for _, d := range data {
		if d != nil && d.Name != "" {
			ps := FromStoreData(d, e.config.LatencyWindowSize, e.config.PenaltyHistorySize)
			e.scores[d.Name] = ps
		}
	}

	// Load latency statistics
	latencyData, err := e.store.GetLatencyStats(ctx)
	if err != nil {
		return fmt.Errorf("failed to load latency stats from store: %w", err)
	}

	if latencyData != nil {
		e.latencyTracker = LatencyTrackerFromStoreData(latencyData, e.config.LatencyWindowSize)
	}

	// Update decay manager with loaded scores
	e.decayManager.UpdateScores(e.scores)

	return nil
}

// SaveToStore persists all current scores to the store.
// This is a full synchronization of in-memory state to persistent storage.
func (e *Engine) SaveToStore(ctx context.Context) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.store == nil {
		return nil // No store configured, nothing to save
	}

	// Convert all scores to store data
	data := AllScoresToStoreData(e.scores)

	// Batch save all scores
	if err := e.store.SetScores(ctx, data); err != nil {
		return fmt.Errorf("failed to save scores to store: %w", err)
	}

	// Save latency statistics
	latencyData := LatencyTrackerToStoreData(e.latencyTracker)
	if latencyData != nil {
		if err := e.store.SetLatencyStats(ctx, latencyData); err != nil {
			return fmt.Errorf("failed to save latency stats to store: %w", err)
		}
	}

	return nil
}

// PersistScore persists a single provider's score to the store.
// This is more efficient than SaveToStore when only one score has changed.
func (e *Engine) PersistScore(ctx context.Context, providerName string) error {
	e.mu.RLock()
	ps, exists := e.scores[providerName]
	e.mu.RUnlock()

	if !exists {
		return fmt.Errorf("provider %s not found", providerName)
	}

	if e.store == nil {
		return nil // No store configured
	}

	data := ToStoreData(ps)
	if err := e.store.SetScore(ctx, data); err != nil {
		return fmt.Errorf("failed to persist score for %s: %w", providerName, err)
	}

	return nil
}
