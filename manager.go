package chainkit

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProviderManager manages multiple providers for a single interface with advanced failure tracking
type ProviderManager struct {
	providers        []ProviderConfig
	selectedProvider string // Specific provider to use, if any
	failureTracker   map[string]*FailureInfo
	circuitStates    map[string]*CircuitState
	healthStates     map[string]*HealthState
	rateLimitStates  map[string]*RateLimitState
	mutex            sync.RWMutex
	config           ChainConfig
	semaphore        chan struct{}         // For controlling concurrency
	selector         selectionStrategy // Selection strategy for choosing providers
}

// FailureInfo tracks failure statistics for a provider with enhanced retry tracking
type FailureInfo struct {
	ConsecutiveFailures int
	LastFailureTime     time.Time
	IsTemporarilyDown   bool
	TotalFailures       int64
	TotalSuccesses      int64
	LastRetryAttempt    time.Time
	RetryCount          int
}

// CircuitState tracks circuit breaker state for a provider
type CircuitState struct {
	State                string // "CLOSED", "OPEN", "HALF_OPEN"
	LastStateChange      time.Time
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	HalfOpenCalls        int
	LastFailureTime      time.Time
}

// HealthState tracks health check state for a provider
type HealthState struct {
	IsHealthy                  bool
	LastHealthCheck            time.Time
	ConsecutiveHealthyChecks   int
	ConsecutiveUnhealthyChecks int
	LastError                  error
}

// RateLimitState tracks rate limiting state for a provider
type RateLimitState struct {
	RequestTimes []time.Time // Sliding window of request timestamps
	BurstTokens  int         // Available burst tokens
	LastRefill   time.Time   // Last time burst tokens were refilled
	mutex        sync.Mutex  // Protects the rate limit state
}

// NewRateLimitState creates a new rate limit state
func NewRateLimitState(burstSize int) *RateLimitState {
	return &RateLimitState{
		RequestTimes: make([]time.Time, 0),
		BurstTokens:  burstSize,
		LastRefill:   time.Now(),
	}
}

// CanMakeRequest checks if a request can be made without violating rate limits
func (rls *RateLimitState) CanMakeRequest(config RateLimitConfig, now time.Time) bool {
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
func (rls *RateLimitState) RecordRequest(config RateLimitConfig, now time.Time) {
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// NewProviderManager creates a new provider manager with chain configuration
func NewProviderManager(config ChainConfig) *ProviderManager {
	semaphore := make(chan struct{}, config.MaxConcurrency)

	pm := &ProviderManager{
		providers:       make([]ProviderConfig, 0),
		failureTracker:  make(map[string]*FailureInfo),
		circuitStates:   make(map[string]*CircuitState),
		healthStates:    make(map[string]*HealthState),
		rateLimitStates: make(map[string]*RateLimitState),
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

func (pm *ProviderManager) HasProvider(name string) bool {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	for _, provider := range pm.providers {
		if provider.Name == name {
			return true
		}
	}
	return false
}

// AddProvider adds a provider with priority
func (pm *ProviderManager) AddProvider(provider interface{}, priority int, name string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// Skip nil providers silently
	if provider == nil {
		return
	}

	pm.providers = append(pm.providers, ProviderConfig{
		Provider: provider,
		Priority: priority,
		Name:     name,
	})

	// Initialize tracking states for new provider
	pm.failureTracker[name] = &FailureInfo{}
	pm.circuitStates[name] = &CircuitState{
		State:           "CLOSED",
		LastStateChange: time.Now(),
	}
	pm.healthStates[name] = &HealthState{
		IsHealthy:       true,
		LastHealthCheck: time.Now(),
	}
	pm.rateLimitStates[name] = NewRateLimitState(pm.config.RateLimit.BurstSize)

	// Sort by priority (lower number = higher priority)
	sort.Slice(pm.providers, func(i, j int) bool {
		return pm.providers[i].Priority < pm.providers[j].Priority
	})
}

// GetAvailableProviders returns providers that are currently available (considering circuit breaker, health, etc.)
// The providers are ordered according to the configured selection strategy.
// Uses a write lock because isProviderAvailable may transition circuit state (OPEN → HALF_OPEN).
func (pm *ProviderManager) GetAvailableProviders() []ProviderConfig {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	available := make([]ProviderConfig, 0)
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

func (pm *ProviderManager) SetSelectedProvider(name string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()
	pm.selectedProvider = name
}

func (pm *ProviderManager) GetSelectedProvider() ProviderConfig {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()
	for _, provider := range pm.providers {
		if provider.Name == pm.selectedProvider {
			return provider
		}
	}
	return ProviderConfig{}
}

func (pm *ProviderManager) ClearSelectedProvider() {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()
	pm.selectedProvider = ""
}

func (pm *ProviderManager) RunOp(ctx context.Context, op func(ctx context.Context, provider interface{}) (interface{}, error)) (data interface{}, providerName string, duration time.Duration, err error) {
	var lastErr error

	// GetAvailableProviders already filters out circuit-open / rate-limited providers.
	providers := pm.GetAvailableProviders()
	if len(providers) == 0 {
		return nil, "", 0, fmt.Errorf("%w: no available providers", ErrProviderNotConfigured)
	}

	// Narrow to a single provider if one is pinned — either via context or via
	// SetSelectedProvider. We always filter from the already-available list so we
	// never try a provider that the circuit breaker has disabled.
	if name, found := GetProviderName(ctx); found {
		// Context-pinned provider: find it inside the available list.
		pinned, ok := findProviderByName(providers, name)
		if !ok {
			return nil, "", 0, fmt.Errorf("context-specified provider %s is not available", name)
		}
		providers = []ProviderConfig{pinned}
	} else {
		// Persistent pin set via SetSelectedProvider.
		pm.mutex.RLock()
		sel := pm.selectedProvider
		pm.mutex.RUnlock()
		if sel != "" {
			pinned, ok := findProviderByName(providers, sel)
			if !ok {
				return nil, "", 0, fmt.Errorf("selected provider %s is not available", sel)
			}
			providers = []ProviderConfig{pinned}
		}
	}

	for _, providerConfig := range providers {
		startTime := time.Now()
		result, err := op(ctx, providerConfig.Provider)
		duration := time.Since(startTime)

		if err == nil {
			pm.RecordSuccess(providerConfig.Name)
			// Record the successful attempt in the selector
			if pm.selector != nil {
				pm.selector.RecordAttempt(providerConfig.Name, providerConfig.Priority)
			}
			return result, providerConfig.Name, duration, nil
		}

		pm.RecordFailure(providerConfig.Name)
		lastErr = err
	}

	return nil, "", 0, fmt.Errorf("all providers failed, last error: %w", lastErr)
}

func (pm *ProviderManager) GetProvider(name string) (ProviderConfig, bool) {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	name = strings.ToLower(name)

	for _, provider := range pm.providers {
		if strings.ToLower(provider.Name) == name {
			return provider, true
		}
	}
	return ProviderConfig{}, false
}

// isProviderAvailable checks if a provider is currently available based on all criteria
func (pm *ProviderManager) isProviderAvailable(providerName string, now time.Time) bool {
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
		if circuitState, exists := pm.circuitStates[providerName]; exists {
			if circuitState.State == "OPEN" {
				// Check if timeout has passed to try half-open
				if now.Sub(circuitState.LastStateChange) >= pm.config.CircuitBreaker.Timeout {
					circuitState.State = "HALF_OPEN"
					circuitState.LastStateChange = now
					circuitState.HalfOpenCalls = 0
				} else {
					return false // Still open
				}
			} else if circuitState.State == "HALF_OPEN" {
				// Limit calls in half-open state
				if circuitState.HalfOpenCalls >= pm.config.CircuitBreaker.HalfOpenMaxCalls {
					return false
				}
			}
		}
	}

	// Check health state
	if pm.config.HealthCheck.Enabled {
		if healthState, exists := pm.healthStates[providerName]; exists {
			if !healthState.IsHealthy {
				return false
			}
		}
	}

	// Check rate limit state
	if pm.config.RateLimit.Enabled {
		if rateLimitState, exists := pm.rateLimitStates[providerName]; exists {
			if !rateLimitState.CanMakeRequest(pm.config.RateLimit, now) {
				return false
			}
		}
	}

	return true
}

// RecordSuccess resets failure tracking and updates circuit breaker state
func (pm *ProviderManager) RecordSuccess(providerName string) {
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
		if circuitState, exists := pm.circuitStates[providerName]; exists {
			circuitState.ConsecutiveFailures = 0
			circuitState.ConsecutiveSuccesses++

			if circuitState.State == "HALF_OPEN" &&
				circuitState.ConsecutiveSuccesses >= pm.config.CircuitBreaker.SuccessThreshold {
				circuitState.State = "CLOSED"
				circuitState.LastStateChange = time.Now()
				circuitState.HalfOpenCalls = 0
			}
		}
	}

	// Update rate limit state
	if pm.config.RateLimit.Enabled {
		if rateLimitState, exists := pm.rateLimitStates[providerName]; exists {
			rateLimitState.RecordRequest(pm.config.RateLimit, time.Now())
		}
	}
}

// RecordFailure records a failure and updates circuit breaker state
func (pm *ProviderManager) RecordFailure(providerName string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	now := time.Now()

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
		pm.failureTracker[providerName] = &FailureInfo{
			ConsecutiveFailures: 1,
			LastFailureTime:     now,
			TotalFailures:       1,
			IsTemporarilyDown:   false,
		}
	}

	// Update circuit breaker state
	if pm.config.CircuitBreaker.Enabled {
		if circuitState, exists := pm.circuitStates[providerName]; exists {
			circuitState.ConsecutiveFailures++
			circuitState.ConsecutiveSuccesses = 0
			circuitState.LastFailureTime = now

			if circuitState.State == "HALF_OPEN" {
				circuitState.HalfOpenCalls++
			}

			// Open circuit if failure threshold reached
			if circuitState.ConsecutiveFailures >= pm.config.CircuitBreaker.FailureThreshold {
				circuitState.State = "OPEN"
				circuitState.LastStateChange = now
			}
		}
	}
}

// GetChainConfig returns the configuration for this provider chain
func (pm *ProviderManager) GetChainConfig() ChainConfig {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()
	return pm.config
}

// UpdateChainConfig updates the configuration for this provider chain
func (pm *ProviderManager) UpdateChainConfig(config ChainConfig) {
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

// AcquireConcurrencySlot attempts to acquire a concurrency slot with timeout
func (pm *ProviderManager) AcquireConcurrencySlot(ctx context.Context) error {
	select {
	case pm.semaphore <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(pm.config.Timeout):
		return fmt.Errorf("timeout waiting for concurrency slot")
	}
}

// ReleaseConcurrencySlot releases a concurrency slot
func (pm *ProviderManager) ReleaseConcurrencySlot() {
	select {
	case <-pm.semaphore:
	default:
		// Slot was already released or never acquired
	}
}

// GetProviderStats returns comprehensive statistics for a provider
func (pm *ProviderManager) GetProviderStats(providerName string) map[string]interface{} {
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

	if circuit, exists := pm.circuitStates[providerName]; exists {
		stats["circuit_state"] = circuit.State
		stats["circuit_consecutive_failures"] = circuit.ConsecutiveFailures
		stats["circuit_consecutive_successes"] = circuit.ConsecutiveSuccesses
		stats["circuit_last_state_change"] = circuit.LastStateChange
		stats["circuit_half_open_calls"] = circuit.HalfOpenCalls
	}

	if health, exists := pm.healthStates[providerName]; exists {
		stats["is_healthy"] = health.IsHealthy
		stats["last_health_check"] = health.LastHealthCheck
		stats["consecutive_healthy_checks"] = health.ConsecutiveHealthyChecks
		stats["consecutive_unhealthy_checks"] = health.ConsecutiveUnhealthyChecks
		if health.LastError != nil {
			stats["last_health_error"] = health.LastError.Error()
		}
	}

	if rateLimit, exists := pm.rateLimitStates[providerName]; exists {
		rateLimit.mutex.Lock()
		stats["rate_limit_request_times"] = append([]time.Time(nil), rateLimit.RequestTimes...)
		stats["rate_limit_burst_tokens"] = rateLimit.BurstTokens
		stats["rate_limit_last_refill"] = rateLimit.LastRefill
		rateLimit.mutex.Unlock()
	}

	return stats
}

// SetSelectionStrategy updates the selection strategy for this provider manager
func (pm *ProviderManager) SetSelectionStrategy(strategy SelectionStrategy) error {
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
func findProviderByName(list []ProviderConfig, name string) (ProviderConfig, bool) {
	lower := strings.ToLower(name)
	for _, p := range list {
		if strings.ToLower(p.Name) == lower {
			return p, true
		}
	}
	return ProviderConfig{}, false
}

// GetSelectionStrategy returns the current selection strategy
func (pm *ProviderManager) GetSelectionStrategy() SelectionStrategy {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()
	return pm.config.SelectionStrategy
}

// ResetSelectionState resets the selection strategy state (useful for round-robin, etc.)
func (pm *ProviderManager) ResetSelectionState() {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if pm.selector != nil {
		pm.selector.Reset()
	}
}
