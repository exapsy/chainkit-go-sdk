package scoring_test

import (
	"context"
	"fmt"
	"time"

	"github.com/exapsy/chainkit/scoring"
)

// Example_basicUsage demonstrates the basic setup and usage of the scoring engine.
func Example_basicUsage() {
	// Create a scoring engine with default configuration
	engine := scoring.NewEngine()

	// Start background processes (decay timer)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine.Start(ctx)
	defer engine.Stop()

	// Register providers with their initial priorities
	// Priority 1 = highest priority, gets base score of 100
	// Priority 2 = gets base score of 90, etc.
	engine.RegisterProvider("mempool", 1)
	engine.RegisterProvider("blockcypher", 2)
	engine.RegisterProvider("blockstream", 3)

	// Check initial scores
	fmt.Printf("Initial scores:\n")
	fmt.Printf("mempool: %.0f\n", engine.GetEffectiveScore("mempool"))
	fmt.Printf("blockcypher: %.0f\n", engine.GetEffectiveScore("blockcypher"))
	fmt.Printf("blockstream: %.0f\n", engine.GetEffectiveScore("blockstream"))

	// Output:
	// Initial scores:
	// mempool: 100
	// blockcypher: 90
	// blockstream: 80
}

// Example_customConfiguration shows how to customize scoring parameters.
func Example_customConfiguration() {
	engine := scoring.NewEngine(
		// Increase penalty for rate limiting (429 errors)
		scoring.WithRateLimitPenalty(30.0),

		// Faster recovery: 20% penalty reduction per minute
		scoring.WithDecayRate(0.2),

		// Severe penalty for authentication failures
		scoring.WithAuthFailurePenalty(80.0),

		// More latency samples for better comparison
		scoring.WithLatencyWindow(200),

		// Only penalize if 2+ standard deviations slower
		scoring.WithSlowThreshold(2.0),
	)

	config := engine.GetConfig()
	fmt.Printf("Rate limit penalty: %.0f\n", config.RateLimitPenalty)
	fmt.Printf("Decay rate: %.0f%%\n", config.DecayRate*100)
	fmt.Printf("Auth failure penalty: %.0f\n", config.AuthFailurePenalty)

	// Output:
	// Rate limit penalty: 30
	// Decay rate: 20%
	// Auth failure penalty: 80
}

// Example_recordingEvents demonstrates how to record events and observe score changes.
func Example_recordingEvents() {
	engine := scoring.NewEngine(
		scoring.WithHealthCheckPenalty(10.0),
		scoring.WithRateLimitPenalty(20.0),
	)

	engine.RegisterProvider("provider1", 1)
	engine.RegisterProvider("provider2", 1) // Same priority

	initialScore := engine.GetEffectiveScore("provider1")
	fmt.Printf("Initial score: %.0f\n", initialScore)

	// Simulate a health check failure
	engine.RecordEvent(scoring.ScoreEvent{
		Type:      scoring.EventHealthCheckFailed,
		Provider:  "provider1",
		Timestamp: time.Now(),
	})

	afterFailure := engine.GetEffectiveScore("provider1")
	fmt.Printf("After health check failure: %.0f\n", afterFailure)

	// Simulate a rate limit (429) response
	engine.RecordEvent(scoring.ScoreEvent{
		Type:       scoring.EventHealthCheck429,
		Provider:   "provider1",
		Timestamp:  time.Now(),
		HTTPStatus: 429,
	})

	afterRateLimit := engine.GetEffectiveScore("provider1")
	fmt.Printf("After rate limit: %.0f\n", afterRateLimit)

	// Compare with provider2 which has no penalties
	provider2Score := engine.GetEffectiveScore("provider2")
	fmt.Printf("Provider2 (no penalties): %.0f\n", provider2Score)

	// Output:
	// Initial score: 100
	// After health check failure: 90
	// After rate limit: 70
	// Provider2 (no penalties): 100
}

// Example_providerOrdering shows how provider ordering changes based on scores.
func Example_providerOrdering() {
	engine := scoring.NewEngine(
		scoring.WithHealthCheckPenalty(15.0),
	)

	// All start with the same priority
	engine.RegisterProvider("providerA", 1)
	engine.RegisterProvider("providerB", 1)
	engine.RegisterProvider("providerC", 1)

	fmt.Println("Initial order:", engine.GetSortedProviders())

	// Penalize providerA
	engine.RecordEvent(scoring.ScoreEvent{
		Type:     scoring.EventHealthCheckFailed,
		Provider: "providerA",
	})
	engine.RecordEvent(scoring.ScoreEvent{
		Type:     scoring.EventHealthCheckFailed,
		Provider: "providerA",
	})

	fmt.Println("After penalizing A:", engine.GetSortedProviders())

	// A has 100 - 30 = 70, while B and C still have 100
	fmt.Printf("Score A: %.0f, B: %.0f, C: %.0f\n",
		engine.GetEffectiveScore("providerA"),
		engine.GetEffectiveScore("providerB"),
		engine.GetEffectiveScore("providerC"))

	// Output depends on map iteration order for B and C, but A will be last
}

// Example_eventClassification shows how to classify events from health check results.
func Example_eventClassification() {
	// Classify a 429 rate limit response
	event429 := scoring.ClassifyHealthCheckEvent(
		"provider1",
		429,                  // HTTP status
		100*time.Millisecond, // Response time
		nil,                  // No error object
	)
	fmt.Printf("429 response → %s\n", event429.Type)

	// Classify an authentication failure
	event401 := scoring.ClassifyHealthCheckEvent(
		"provider1",
		401,
		50*time.Millisecond,
		nil,
	)
	fmt.Printf("401 response → %s\n", event401.Type)

	// Classify a successful response
	event200 := scoring.ClassifyHealthCheckEvent(
		"provider1",
		200,
		30*time.Millisecond,
		nil,
	)
	fmt.Printf("200 response → %s\n", event200.Type)

	// Classify a server error
	event500 := scoring.ClassifyHealthCheckEvent(
		"provider1",
		500,
		200*time.Millisecond,
		nil,
	)
	fmt.Printf("500 response → %s\n", event500.Type)

	// Output:
	// 429 response → healthcheck_429
	// 401 response → healthcheck_auth
	// 200 response → operation_success
	// 500 response → healthcheck_failed
}

// Example_monitoringStats demonstrates how to monitor provider statistics.
func Example_monitoringStats() {
	engine := scoring.NewEngine()
	engine.RegisterProvider("provider1", 1)

	// Simulate some operations
	for i := 0; i < 8; i++ {
		engine.RecordEvent(scoring.ScoreEvent{
			Type:         scoring.EventOperationSuccess,
			Provider:     "provider1",
			ResponseTime: time.Duration(100+i*10) * time.Millisecond,
		})
	}

	// Simulate 2 failures
	engine.RecordEvent(scoring.ScoreEvent{
		Type:     scoring.EventOperationFailed,
		Provider: "provider1",
	})
	engine.RecordEvent(scoring.ScoreEvent{
		Type:     scoring.EventOperationFailed,
		Provider: "provider1",
	})

	// Get statistics
	stats := engine.GetProviderStats("provider1")
	if stats != nil {
		fmt.Printf("Total operations: %d\n", stats.TotalOperations)
		fmt.Printf("Successful: %d\n", stats.SuccessfulOps)
		fmt.Printf("Failed: %d\n", stats.FailedOps)
		fmt.Printf("Success rate: %.0f%%\n", stats.SuccessRate*100)
	}

	// Output:
	// Total operations: 10
	// Successful: 8
	// Failed: 2
	// Success rate: 80%
}

// Example_disableAndEnable shows how to disable and re-enable scoring at runtime.
func Example_disableAndEnable() {
	engine := scoring.NewEngine(
		scoring.WithHealthCheckPenalty(20.0),
	)
	engine.RegisterProvider("provider1", 1)

	initialScore := engine.GetEffectiveScore("provider1")

	// Disable scoring
	engine.SetEnabled(false)

	// Events are ignored when disabled
	engine.RecordEvent(scoring.ScoreEvent{
		Type:     scoring.EventHealthCheckFailed,
		Provider: "provider1",
	})

	scoreWhileDisabled := engine.GetEffectiveScore("provider1")

	fmt.Printf("Score unchanged while disabled: %v\n", initialScore == scoreWhileDisabled)

	// Re-enable scoring
	engine.SetEnabled(true)

	// Now events are processed
	engine.RecordEvent(scoring.ScoreEvent{
		Type:     scoring.EventHealthCheckFailed,
		Provider: "provider1",
	})

	scoreAfterEnabled := engine.GetEffectiveScore("provider1")
	fmt.Printf("Score changed after re-enabling: %v\n", scoreAfterEnabled < initialScore)

	// Output:
	// Score unchanged while disabled: true
	// Score changed after re-enabling: true
}

// Example_latencyTracking demonstrates relative latency tracking between providers.
func Example_latencyTracking() {
	engine := scoring.NewEngine(
		scoring.WithSlowResponsePenalty(10.0),
		scoring.WithSlowThreshold(0.5), // Lower threshold for more sensitive detection
		scoring.WithMinLatencySamples(3),
	)

	engine.RegisterProvider("fast_provider", 1)
	engine.RegisterProvider("slow_provider", 1)

	// Record latencies: fast provider is consistently fast
	for i := 0; i < 10; i++ {
		engine.RecordEvent(scoring.ScoreEvent{
			Type:         scoring.EventOperationSuccess,
			Provider:     "fast_provider",
			ResponseTime: 50 * time.Millisecond,
		})

		// Slow provider is 10x slower
		engine.RecordEvent(scoring.ScoreEvent{
			Type:         scoring.EventOperationSuccess,
			Provider:     "slow_provider",
			ResponseTime: 500 * time.Millisecond,
		})
	}

	// Check latency statistics
	globalStats := engine.GetLatencyStats()
	fmt.Printf("Global mean latency: %v\n", globalStats.Mean)

	fastStats := engine.GetProviderLatencyStats("fast_provider")
	slowStats := engine.GetProviderLatencyStats("slow_provider")

	fmt.Printf("Fast provider avg: %v\n", fastStats.Mean)
	fmt.Printf("Slow provider avg: %v\n", slowStats.Mean)

	// Slow provider should have a lower effective score due to latency penalty
	fastScore := engine.GetEffectiveScore("fast_provider")
	slowScore := engine.GetEffectiveScore("slow_provider")

	fmt.Printf("Fast provider score > Slow provider score: %v\n", fastScore > slowScore)

	// Output:
	// Global mean latency: 275ms
	// Fast provider avg: 50ms
	// Slow provider avg: 500ms
	// Fast provider score > Slow provider score: true
}
