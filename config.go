package chainkit

import "time"

// RetryPolicy defines how retries should be handled for a provider chain
type RetryPolicy struct {
	Enabled           bool          // Whether retries are enabled
	MaxAttempts       int           // Maximum number of retry attempts per operation
	InitialDelay      time.Duration // Initial delay before first retry
	MaxDelay          time.Duration // Maximum delay between retries
	BackoffMultiplier float64       // Multiplier for exponential backoff (e.g., 2.0 for doubling)
	Jitter            bool          // Whether to add random jitter to delays
}

// RateLimitConfig defines rate limiting behavior for providers
type RateLimitConfig struct {
	Enabled     bool          // Whether rate limiting is enabled
	MaxRequests int           // Maximum requests allowed in the time window
	Window      time.Duration // Time window for rate limiting (e.g., 1 minute)
	BurstSize   int           // Allow burst of requests above the rate limit (optional)
}

// CircuitBreakerConfig defines circuit breaker behavior for a provider chain
type CircuitBreakerConfig struct {
	Enabled                bool          // Whether circuit breaker is enabled
	FailureThreshold       int           // Number of consecutive failures before opening circuit
	SuccessThreshold       int           // Number of consecutive successes needed to close circuit
	Timeout                time.Duration // How long circuit stays open before attempting to close
	HalfOpenMaxCalls       int           // Maximum calls allowed in half-open state
	RecoveryWindowDuration time.Duration // Time window for measuring success/failure rates
}

// HealthCheckConfig defines health check behavior for providers
type HealthCheckConfig struct {
	Enabled            bool          // Whether health checks are enabled
	Interval           time.Duration // How often to run health checks
	Timeout            time.Duration // Timeout for individual health check calls
	UnhealthyThreshold int           // Number of failed health checks before marking unhealthy
	HealthyThreshold   int           // Number of successful health checks before marking healthy
}

// ChainConfig holds configuration for a specific provider chain
type ChainConfig struct {
	Name              string               // Name of the chain (e.g., "BalanceFetchers", "TxBroadcasters")
	RetryPolicy       RetryPolicy          // Retry configuration for this chain
	CircuitBreaker    CircuitBreakerConfig // Circuit breaker configuration
	RateLimit         RateLimitConfig      // Rate limiting configuration for this chain
	Timeout           time.Duration        // Global timeout for operations in this chain
	MaxConcurrency    int                  // Maximum concurrent operations for this chain
	HealthCheck       HealthCheckConfig    // Health check configuration
	SelectionStrategy SelectionStrategy    // Strategy for selecting providers (priority_only, round_robin, etc.)
}

// DefaultChainConfig returns a sensible default configuration for provider chains
func DefaultChainConfig(chainType ProviderChainType) ChainConfig {
	return ChainConfig{
		Name: chainType.String(),
		RetryPolicy: RetryPolicy{
			Enabled:           true,
			MaxAttempts:       3,
			InitialDelay:      100 * time.Millisecond,
			MaxDelay:          5 * time.Second,
			BackoffMultiplier: 2.0,
			Jitter:            true,
		},
		CircuitBreaker: CircuitBreakerConfig{
			Enabled:                true,
			FailureThreshold:       5,
			SuccessThreshold:       3,
			Timeout:                30 * time.Second,
			HalfOpenMaxCalls:       3,
			RecoveryWindowDuration: 60 * time.Second,
		},
		Timeout:        30 * time.Second,
		MaxConcurrency: 10,
		HealthCheck: HealthCheckConfig{
			Enabled:            false, // Disabled by default
			Interval:           60 * time.Second,
			Timeout:            5 * time.Second,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
		RateLimit: RateLimitConfig{
			Enabled:     false, // Disabled by default
			MaxRequests: 100,
			Window:      1 * time.Minute,
			BurstSize:   0, // No burst allowed by default
		},
		SelectionStrategy: SelectionStrategyPriorityOnly, // Default to priority-only (current behavior)
	}
}

// ProviderConfig configuration structs for each provider type
type ProviderConfig struct {
	Provider interface{}
	Priority int // Lower number = higher priority
	Name     string
}

type BalanceFetcherConfig struct {
	Fetcher     BalanceFetcher
	Priority    int
	Name        string       // Optional - if empty, will be derived from Fetcher.Name()
	ChainConfig *ChainConfig // Optional - if provided, will be applied to the chain
}

type RateFetcherConfig struct {
	Fetcher     RateFetcher
	Priority    int
	Name        string       // Optional - if empty, will be derived from Fetcher.Name()
	ChainConfig *ChainConfig // Optional - if provided, will be applied to the chain
}

type AddressGeneratorConfig struct {
	Generator   AddressGenerator
	Priority    int
	Name        string       // Optional - if empty, will be derived from Generator.Name()
	ChainConfig *ChainConfig // Optional - if provided, will be applied to the chain
}

type AddressValidatorConfig struct {
	Validator   AddressValidator
	Priority    int
	Name        string       // Optional - if empty, will be derived from Validator.Name()
	ChainConfig *ChainConfig // Optional - if provided, will be applied to the chain
}

type FeeRecommenderConfig struct {
	Recommender FeeRecommender
	Priority    int
	Name        string       // Optional - if empty, will be derived from Recommender.Name()
	ChainConfig *ChainConfig // Optional - if provided, will be applied to the chain
}

type FeeEstimatorConfig struct {
	Estimator   FeeEstimator
	Priority    int
	Name        string       // Optional - if empty, will be derived from Estimator.Name()
	ChainConfig *ChainConfig // Optional - if provided, will be applied to the chain
}

type TxBroadcasterConfig struct {
	Broadcaster TxBroadcaster
	Priority    int
	Name        string       // Optional - if empty, will be derived from Broadcaster.Name()
	ChainConfig *ChainConfig // Optional - if provided, will be applied to the chain
}

type TxAssemblerConfig struct {
	Assembler   TxAssembler
	Priority    int
	Name        string       // Optional - if empty, will be derived from Assembler.Name()
	ChainConfig *ChainConfig // Optional - if provided, will be applied to the chain
}

type TxSizerConfig struct {
	Sizer       TxSizer
	Priority    int
	Name        string       // Optional - if empty, will be derived from Sizer.Name()
	ChainConfig *ChainConfig // Optional - if provided, will be applied to the chain
}

type TxSignerConfig struct {
	Signer      TxSigner
	Priority    int
	Name        string       // Optional - if empty, will be derived from Signer.Name()
	ChainConfig *ChainConfig // Optional - if provided, will be applied to the chain
}

type UTXOFetcherConfig struct {
	Fetcher     UTXOFetcher
	Priority    int
	Name        string       // Optional - if empty, will be derived from Fetcher.Name()
	ChainConfig *ChainConfig // Optional - if provided, will be applied to the chain
}
