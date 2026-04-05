package chainkit

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/exapsy/chainkit/scoring"
)

// providerManager manages multiple providers for a single interface with advanced failure tracking
type providerManager struct {
	providers        []providerConfig
	selectedProvider string // Specific provider to use, if any
	failureTracker   map[string]*failureInfo
	circuitStates    map[string]*circuitState
	healthStates     map[string]*healthState
	rateLimitStates  map[string]*rateLimitState
	mutex            sync.RWMutex
	config           ChainConfig
	semaphore        chan struct{}     // For controlling concurrency
	selector         selectionStrategy // Selection strategy for choosing providers
	scoringEngine    *scoring.Engine   // Optional adaptive scoring engine
}

// failureInfo tracks failure statistics for a provider with enhanced retry tracking
type failureInfo struct {
	ConsecutiveFailures int
	LastFailureTime     time.Time
	IsTemporarilyDown   bool
	TotalFailures       int64
	TotalSuccesses      int64
	LastRetryAttempt    time.Time
	RetryCount          int
}

// circuitState tracks circuit breaker state for a provider
type circuitState struct {
	State                string // "CLOSED", "OPEN", "HALF_OPEN"
	LastStateChange      time.Time
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	HalfOpenCalls        int
	LastFailureTime      time.Time
}

// healthState tracks health check state for a provider
type healthState struct {
	IsHealthy                  bool
	LastHealthCheck            time.Time
	ConsecutiveHealthyChecks   int
	ConsecutiveUnhealthyChecks int
	LastError                  error
}

// rateLimitState tracks rate limiting state for a provider
type rateLimitState struct {
	RequestTimes []time.Time // Sliding window of request timestamps
	BurstTokens  int         // Available burst tokens
	LastRefill   time.Time   // Last time burst tokens were refilled
	mutex        sync.Mutex  // Protects the rate limit state
}

// newRateLimitState creates a new rate limit state
func newRateLimitState(burstSize int) *rateLimitState {
	return &rateLimitState{
		RequestTimes: make([]time.Time, 0),
		BurstTokens:  burstSize,
		LastRefill:   time.Now(),
	}
}

// CanMakeRequest checks if a request can be made without violating rate limits
func (rls *rateLimitState) CanMakeRequest(config RateLimitConfig, now time.Time) bool {
	if !config.Enabled {
		return true
	}

	rls.mutex.Lock()
	defer rls.mutex.Unlock()

	// Clean old requests outside the time window
	windowStart := now.Add(-config.Window)
	validRequests := make([]time.Time, 0)
	for _, reqTime := range rls.RequestTimes {
		if reqTime.After(windowStart) {
			validRequests = append(validRequests, reqTime)
		}
	}
	rls.RequestTimes = validRequests

	// Check if we're within the rate limit
	if len(rls.RequestTimes) < config.MaxRequests {
		return true
	}

	// Check burst tokens if burst is enabled
	if config.BurstSize > 0 {
		// Refill burst tokens over time (simple token bucket)
		timeSinceRefill := now.Sub(rls.LastRefill)
		refillInterval := config.Window / time.Duration(config.MaxRequests)
		tokensToAdd := int(timeSinceRefill / refillInterval)

		if tokensToAdd > 0 {
			rls.BurstTokens = minInt(rls.BurstTokens+tokensToAdd, config.BurstSize)
			rls.LastRefill = now
		}

		if rls.BurstTokens > 0 {
			// Consume the token atomically here so concurrent callers
			// cannot both observe the same non-zero count and both proceed.
			rls.BurstTokens--
			return true
		}
	}

	return false
}

// RecordRequest records a dispatched request for rate-window tracking.
// Burst token consumption is handled atomically inside CanMakeRequest.
func (rls *rateLimitState) RecordRequest(config RateLimitConfig, now time.Time) {
	if !config.Enabled {
		return
	}

	rls.mutex.Lock()
	defer rls.mutex.Unlock()

	// Add the request timestamp for the sliding-window count.
	rls.RequestTimes = append(rls.RequestTimes, now)
}

// Helper functions for min/max (using different names to avoid builtin collision)
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// newProviderManager creates a new provider manager with chain configuration
func newProviderManager(config ChainConfig) *providerManager {
	semaphore := make(chan struct{}, config.MaxConcurrency)

	pm := &providerManager{
		providers:       make([]providerConfig, 0),
		failureTracker:  make(map[string]*failureInfo),
		circuitStates:   make(map[string]*circuitState),
		healthStates:    make(map[string]*healthState),
		rateLimitStates: make(map[string]*rateLimitState),
		config:          config,
		semaphore:       semaphore,
	}

	// Initialize the selector based on the configured strategy
	selector, err := newSelectionStrategy(config.SelectionStrategy, pm.failureTracker)
	if err != nil {
		// Fall back to priority-only if there's an error
		selector = newPriorityOnlySelector()
	}
	pm.selector = selector

	return pm
}

func (pm *providerManager) HasProvider(name string) bool {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	for _, provider := range pm.providers {
		if provider.Name == name {
			return true
		}
	}
	return false
}

// addProvider adds a provider with priority
func (pm *providerManager) addProvider(provider interface{}, priority int, name string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// Skip nil providers silently
	if provider == nil {
		return
	}

	pm.providers = append(pm.providers, providerConfig{
		Provider: provider,
		Priority: priority,
		Name:     name,
	})

	// Initialize tracking states for new provider
	pm.failureTracker[name] = &failureInfo{}
	pm.circuitStates[name] = &circuitState{
		State:           "CLOSED",
		LastStateChange: time.Now(),
	}
	pm.healthStates[name] = &healthState{
		IsHealthy:       true,
		LastHealthCheck: time.Now(),
	}
	pm.rateLimitStates[name] = newRateLimitState(pm.config.RateLimit.BurstSize)

	// Sort by priority (lower number = higher priority)
	sort.Slice(pm.providers, func(i, j int) bool {
		return pm.providers[i].Priority < pm.providers[j].Priority
	})
}

// getAvailableProviders returns providers that are currently available (considering circuit breaker, health, etc.)
// The providers are ordered according to the configured selection strategy.
// Uses a write lock because isProviderAvailable may transition circuit state (OPEN → HALF_OPEN).
func (pm *providerManager) getAvailableProviders() []providerConfig {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	available := make([]providerConfig, 0)
	now := time.Now()

	for _, provider := range pm.providers {
		if !pm.isProviderAvailable(provider.Name, now) {
			continue
		}
		available = append(available, provider)
	}

	// Apply selection strategy to order the available providers
	if pm.selector != nil {
		available = pm.selector.SelectProviders(available)
	}

	return available
}

func (pm *providerManager) runOp(ctx context.Context, op func(ctx context.Context, provider interface{}) (interface{}, error)) (data interface{}, providerName string, duration time.Duration, err error) {
	var lastErr error

	// getAvailableProviders already filters out circuit-open / rate-limited providers.
	providers := pm.getAvailableProviders()
	if len(providers) == 0 {
		return nil, "", 0, fmt.Errorf("%w: no available providers", ErrProviderNotConfigured)
	}

	// Narrow to a single provider if one is pinned — either via context or via
	// setSelectedProvider. We always filter from the already-available list so we
	// never try a provider that the circuit breaker has disabled.
	if name, found := GetProviderName(ctx); found {
		// Context-pinned provider: find it inside the available list.
		pinned, ok := findProviderByName(providers, name)
		if !ok {
			return nil, "", 0, fmt.Errorf("context-specified provider %s is not available", name)
		}
		providers = []providerConfig{pinned}
	} else {
		// Persistent pin set via setSelectedProvider.
		pm.mutex.RLock()
		sel := pm.selectedProvider
		pm.mutex.RUnlock()
		if sel != "" {
			pinned, ok := findProviderByName(providers, sel)
			if !ok {
				return nil, "", 0, fmt.Errorf("selected provider %s is not available", sel)
			}
			providers = []providerConfig{pinned}
		}
	}

	retry := pm.config.RetryPolicy

	for _, providerCfg := range providers {
		result, opErr := pm.attemptWithRetry(ctx, providerCfg, op, retry)
		if opErr == nil {
			if pm.selector != nil {
				pm.selector.RecordAttempt(providerCfg.Name, providerCfg.Priority)
			}
			// duration is captured inside attemptWithRetry; surface the total wall time.
			return result, providerCfg.Name, 0, nil
		}

		recordFailedProvider(ctx, providerCfg.Name)
		lastErr = opErr
	}

	return nil, "", 0, fmt.Errorf("all providers failed, last error: %w", lastErr)
}

// attemptWithRetry runs op against a single provider, retrying according to
// policy. It records success/failure on the providerManager after each attempt.
func (pm *providerManager) attemptWithRetry(
	ctx context.Context,
	providerCfg providerConfig,
	op func(ctx context.Context, provider interface{}) (interface{}, error),
	policy RetryPolicy,
) (interface{}, error) {
	maxAttempts := 1
	if policy.Enabled && policy.MaxAttempts > 1 {
		maxAttempts = policy.MaxAttempts
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := pm.retryDelay(policy, attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		start := time.Now()
		result, err := op(ctx, providerCfg.Provider)
		elapsed := time.Since(start)
		if err == nil {
			pm.recordSuccessWithLatency(providerCfg.Name, elapsed)
			return result, nil
		}

		pm.recordFailureWithError(providerCfg.Name, err, elapsed)
		lastErr = err
	}

	return nil, lastErr
}

// retryDelay computes the exponential-backoff delay for the given attempt number
// (1-based: attempt=1 is the first retry after the initial failure).
func (pm *providerManager) retryDelay(policy RetryPolicy, attempt int) time.Duration {
	delay := float64(policy.InitialDelay) * math.Pow(policy.BackoffMultiplier, float64(attempt-1))
	if policy.MaxDelay > 0 && time.Duration(delay) > policy.MaxDelay {
		delay = float64(policy.MaxDelay)
	}
	if policy.Jitter {
		// Add up to 20 % random jitter.
		delay *= 1 + 0.2*rand.Float64()
	}
	return time.Duration(delay)
}

// isProviderAvailable checks if a provider is currently available based on all criteria
func (pm *providerManager) isProviderAvailable(providerName string, now time.Time) bool {
	// Check legacy failure tracking
	if failure, exists := pm.failureTracker[providerName]; exists {
		if failure.IsTemporarilyDown {
			// Check if cooldown period has passed
			if now.Sub(failure.LastFailureTime) >= pm.config.CircuitBreaker.Timeout {
				failure.IsTemporarilyDown = false
				failure.ConsecutiveFailures = 0
			} else {
				return false
			}
		}
	}

	// Check circuit breaker state
	if pm.config.CircuitBreaker.Enabled {
		if cs, exists := pm.circuitStates[providerName]; exists {
			if cs.State == "OPEN" {
				// Check if timeout has passed to try half-open
				if now.Sub(cs.LastStateChange) >= pm.config.CircuitBreaker.Timeout {
					cs.State = "HALF_OPEN"
					cs.LastStateChange = now
					cs.HalfOpenCalls = 0
				} else {
					return false // Still open
				}
			} else if cs.State == "HALF_OPEN" {
				// Limit calls in half-open state
				if cs.HalfOpenCalls >= pm.config.CircuitBreaker.HalfOpenMaxCalls {
					return false
				}
			}
		}
	}

	// Check health state
	if pm.config.HealthCheck.Enabled {
		if hs, exists := pm.healthStates[providerName]; exists {
			if !hs.IsHealthy {
				return false
			}
		}
	}

	// Check rate limit state
	if pm.config.RateLimit.Enabled {
		if rls, exists := pm.rateLimitStates[providerName]; exists {
			if !rls.CanMakeRequest(pm.config.RateLimit, now) {
				return false
			}
		}
	}

	return true
}

// recordSuccess resets failure tracking and updates circuit breaker state
func (pm *providerManager) recordSuccess(providerName string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// Update failure tracking
	if failure, exists := pm.failureTracker[providerName]; exists {
		failure.ConsecutiveFailures = 0
		failure.IsTemporarilyDown = false
		failure.TotalSuccesses++
		failure.RetryCount = 0
	}

	// Update circuit breaker state
	if pm.config.CircuitBreaker.Enabled {
		if cs, exists := pm.circuitStates[providerName]; exists {
			cs.ConsecutiveFailures = 0
			cs.ConsecutiveSuccesses++

			if cs.State == "HALF_OPEN" &&
				cs.ConsecutiveSuccesses >= pm.config.CircuitBreaker.SuccessThreshold {
				cs.State = "CLOSED"
				cs.LastStateChange = time.Now()
				cs.HalfOpenCalls = 0
			}
		}
	}

	// Update rate limit state
	if pm.config.RateLimit.Enabled {
		if rls, exists := pm.rateLimitStates[providerName]; exists {
			rls.RecordRequest(pm.config.RateLimit, time.Now())
		}
	}

	// Record success event in scoring engine
	if pm.scoringEngine != nil {
		pm.scoringEngine.RecordEvent(scoring.ScoreEvent{
			Type:      scoring.EventOperationSuccess,
			Provider:  providerName,
			Timestamp: time.Now(),
		})
	}
}

// updateFailureState updates failure tracker and circuit breaker for a failed operation.
// It does NOT emit a scoring event — callers are responsible for that.
// Must NOT be called with pm.mutex held.
func (pm *providerManager) updateFailureState(providerName string, now time.Time) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// Update failure tracking
	if failure, exists := pm.failureTracker[providerName]; exists {
		failure.ConsecutiveFailures++
		failure.LastFailureTime = now
		failure.TotalFailures++

		// Legacy behavior: mark as temporarily down after threshold
		if failure.ConsecutiveFailures >= pm.config.CircuitBreaker.FailureThreshold {
			failure.IsTemporarilyDown = true
		}
	} else {
		pm.failureTracker[providerName] = &failureInfo{
			ConsecutiveFailures: 1,
			LastFailureTime:     now,
			TotalFailures:       1,
			IsTemporarilyDown:   false,
		}
	}

	// Update circuit breaker state
	if pm.config.CircuitBreaker.Enabled {
		if cs, exists := pm.circuitStates[providerName]; exists {
			cs.ConsecutiveFailures++
			cs.ConsecutiveSuccesses = 0
			cs.LastFailureTime = now

			if cs.State == "HALF_OPEN" {
				cs.HalfOpenCalls++
			}

			// Open circuit if failure threshold reached
			if cs.ConsecutiveFailures >= pm.config.CircuitBreaker.FailureThreshold {
				cs.State = "OPEN"
				cs.LastStateChange = now
			}
		}
	}
}

// updateChainConfig updates the configuration for this provider chain
func (pm *providerManager) updateChainConfig(config ChainConfig) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	oldStrategy := pm.config.SelectionStrategy
	pm.config = config

	// Resize semaphore if concurrency limit changed
	if cap(pm.semaphore) != config.MaxConcurrency {
		pm.semaphore = make(chan struct{}, config.MaxConcurrency)
	}

	// Update selector if strategy changed
	if oldStrategy != config.SelectionStrategy {
		selector, err := newSelectionStrategy(config.SelectionStrategy, pm.failureTracker)
		if err != nil {
			// Fall back to priority-only if there's an error
			selector = newPriorityOnlySelector()
		}
		pm.selector = selector
	}
}

// GetProviderStats returns comprehensive statistics for a provider
func (pm *providerManager) GetProviderStats(providerName string) map[string]interface{} {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	stats := make(map[string]interface{})

	if failure, exists := pm.failureTracker[providerName]; exists {
		stats["consecutive_failures"] = failure.ConsecutiveFailures
		stats["total_failures"] = failure.TotalFailures
		stats["total_successes"] = failure.TotalSuccesses
		stats["is_temporarily_down"] = failure.IsTemporarilyDown
		stats["last_failure_time"] = failure.LastFailureTime
		stats["retry_count"] = failure.RetryCount
	}

	if cs, exists := pm.circuitStates[providerName]; exists {
		stats["circuit_state"] = cs.State
		stats["circuit_consecutive_failures"] = cs.ConsecutiveFailures
		stats["circuit_consecutive_successes"] = cs.ConsecutiveSuccesses
		stats["circuit_last_state_change"] = cs.LastStateChange
		stats["circuit_half_open_calls"] = cs.HalfOpenCalls
	}

	if hs, exists := pm.healthStates[providerName]; exists {
		stats["is_healthy"] = hs.IsHealthy
		stats["last_health_check"] = hs.LastHealthCheck
		stats["consecutive_healthy_checks"] = hs.ConsecutiveHealthyChecks
		stats["consecutive_unhealthy_checks"] = hs.ConsecutiveUnhealthyChecks
		if hs.LastError != nil {
			stats["last_health_error"] = hs.LastError.Error()
		}
	}

	if rls, exists := pm.rateLimitStates[providerName]; exists {
		rls.mutex.Lock()
		stats["rate_limit_request_times"] = append([]time.Time(nil), rls.RequestTimes...)
		stats["rate_limit_burst_tokens"] = rls.BurstTokens
		stats["rate_limit_last_refill"] = rls.LastRefill
		rls.mutex.Unlock()
	}

	return stats
}

// SetSelectionStrategy updates the selection strategy for this provider manager
func (pm *providerManager) SetSelectionStrategy(strategy SelectionStrategy) error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if !strategy.IsValid() {
		return fmt.Errorf("invalid selection strategy: %s", strategy)
	}

	selector, err := newSelectionStrategy(strategy, pm.failureTracker)
	if err != nil {
		return err
	}

	pm.selector = selector
	pm.config.SelectionStrategy = strategy
	return nil
}

// findProviderByName returns the first provider in list whose name matches (case-insensitive).
func findProviderByName(list []providerConfig, name string) (providerConfig, bool) {
	lower := strings.ToLower(name)
	for _, p := range list {
		if strings.ToLower(p.Name) == lower {
			return p, true
		}
	}
	return providerConfig{}, false
}

// GetSelectionStrategy returns the current selection strategy
func (pm *providerManager) GetSelectionStrategy() SelectionStrategy {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()
	return pm.config.SelectionStrategy
}

// SetScoringEngine sets the adaptive scoring engine and switches to adaptive selection
func (pm *providerManager) SetScoringEngine(engine *scoring.Engine) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	pm.scoringEngine = engine

	if engine != nil {
		// Register all current providers with the scoring engine
		for _, p := range pm.providers {
			engine.RegisterProvider(p.Name, p.Priority)
		}

		// Switch to adaptive selector
		pm.selector = newAdaptiveSelector(engine)
		pm.config.SelectionStrategy = SelectionStrategyAdaptive
	}
}

// GetScoringEngine returns the current scoring engine (may be nil)
func (pm *providerManager) GetScoringEngine() *scoring.Engine {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()
	return pm.scoringEngine
}

// recordFailureWithError records a failure with error classification for scoring.
// Unlike recordFailure, it emits only the classified event (not a generic EventOperationFailed
// first), so a single operation failure produces exactly one scoring event.
func (pm *providerManager) recordFailureWithError(providerName string, err error, responseTime time.Duration) {
	now := time.Now()
	pm.updateFailureState(providerName, now)

	if pm.scoringEngine != nil {
		event := scoring.ClassifyOperationEvent(providerName, responseTime, err)
		event.Timestamp = now
		pm.scoringEngine.RecordEvent(event)
	}
}

// recordSuccessWithLatency records a success with latency for scoring
func (pm *providerManager) recordSuccessWithLatency(providerName string, responseTime time.Duration) {
	// First, record the basic success
	pm.recordSuccess(providerName)

	// Then, if we have a scoring engine, record the latency
	if pm.scoringEngine != nil {
		event := scoring.ScoreEvent{
			Type:         scoring.EventOperationSuccess,
			Provider:     providerName,
			Timestamp:    time.Now(),
			ResponseTime: responseTime,
		}
		pm.scoringEngine.RecordEvent(event)
	}
}

// RecordHealthCheckResult records a health check result for scoring
func (pm *providerManager) RecordHealthCheckResult(providerName string, httpStatus int, responseTime time.Duration, err error) {
	if pm.scoringEngine == nil {
		return
	}

	event := scoring.ClassifyHealthCheckEvent(providerName, httpStatus, responseTime, err)
	pm.scoringEngine.RecordEvent(event)
}
