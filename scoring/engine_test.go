package scoring

import (
	"context"
	"testing"
	"time"
)

func TestNewEngine(t *testing.T) {
	t.Run("creates engine with default config", func(t *testing.T) {
		engine := NewEngine()
		if engine == nil {
			t.Fatal("expected engine to be created")
		}

		config := engine.GetConfig()
		if !config.Enabled {
			t.Error("expected engine to be enabled by default")
		}
		if config.RateLimitPenalty != 20.0 {
			t.Errorf("expected default rate limit penalty of 20.0, got %f", config.RateLimitPenalty)
		}
	})

	t.Run("applies custom options", func(t *testing.T) {
		engine := NewEngine(
			WithRateLimitPenalty(30.0),
			WithDecayRate(0.2),
			WithHealthCheckPenalty(10.0),
		)

		config := engine.GetConfig()
		if config.RateLimitPenalty != 30.0 {
			t.Errorf("expected rate limit penalty of 30.0, got %f", config.RateLimitPenalty)
		}
		if config.DecayRate != 0.2 {
			t.Errorf("expected decay rate of 0.2, got %f", config.DecayRate)
		}
		if config.HealthCheckFailPenalty != 10.0 {
			t.Errorf("expected health check penalty of 10.0, got %f", config.HealthCheckFailPenalty)
		}
	})
}

func TestEngine_RegisterProvider(t *testing.T) {
	engine := NewEngine()

	engine.RegisterProvider("provider1", 1)
	engine.RegisterProvider("provider2", 2)
	engine.RegisterProvider("provider3", 3)

	if engine.GetProviderCount() != 3 {
		t.Errorf("expected 3 providers, got %d", engine.GetProviderCount())
	}

	if !engine.HasProvider("provider1") {
		t.Error("expected provider1 to be registered")
	}

	// Test that priority affects base score
	score1 := engine.GetEffectiveScore("provider1")
	score2 := engine.GetEffectiveScore("provider2")
	score3 := engine.GetEffectiveScore("provider3")

	if score1 <= score2 {
		t.Errorf("expected provider1 (priority 1) to have higher score than provider2 (priority 2), got %f <= %f", score1, score2)
	}
	if score2 <= score3 {
		t.Errorf("expected provider2 (priority 2) to have higher score than provider3 (priority 3), got %f <= %f", score2, score3)
	}
}

func TestEngine_UnregisterProvider(t *testing.T) {
	engine := NewEngine()

	engine.RegisterProvider("provider1", 1)
	engine.RegisterProvider("provider2", 2)

	engine.UnregisterProvider("provider1")

	if engine.HasProvider("provider1") {
		t.Error("expected provider1 to be unregistered")
	}
	if !engine.HasProvider("provider2") {
		t.Error("expected provider2 to still be registered")
	}
	if engine.GetProviderCount() != 1 {
		t.Errorf("expected 1 provider, got %d", engine.GetProviderCount())
	}
}

func TestEngine_RecordEvent_HealthCheckFailure(t *testing.T) {
	engine := NewEngine(WithHealthCheckPenalty(5.0))
	engine.RegisterProvider("provider1", 1)

	initialScore := engine.GetEffectiveScore("provider1")

	// Record a health check failure
	engine.RecordEvent(ScoreEvent{
		Type:      EventHealthCheckFailed,
		Provider:  "provider1",
		Timestamp: time.Now(),
	})

	newScore := engine.GetEffectiveScore("provider1")

	expectedDrop := 5.0
	actualDrop := initialScore - newScore

	if actualDrop < expectedDrop-0.1 || actualDrop > expectedDrop+0.1 {
		t.Errorf("expected score to drop by ~%f, but dropped by %f (from %f to %f)",
			expectedDrop, actualDrop, initialScore, newScore)
	}
}

func TestEngine_RecordEvent_RateLimit429(t *testing.T) {
	engine := NewEngine(WithRateLimitPenalty(20.0))
	engine.RegisterProvider("provider1", 1)

	initialScore := engine.GetEffectiveScore("provider1")

	// Record a 429 rate limit event
	engine.RecordEvent(ScoreEvent{
		Type:       EventHealthCheck429,
		Provider:   "provider1",
		Timestamp:  time.Now(),
		HTTPStatus: 429,
	})

	newScore := engine.GetEffectiveScore("provider1")

	expectedDrop := 20.0
	actualDrop := initialScore - newScore

	if actualDrop < expectedDrop-0.1 || actualDrop > expectedDrop+0.1 {
		t.Errorf("expected score to drop by ~%f, but dropped by %f", expectedDrop, actualDrop)
	}
}

func TestEngine_RecordEvent_AuthFailure(t *testing.T) {
	engine := NewEngine(WithAuthFailurePenalty(50.0))
	engine.RegisterProvider("provider1", 1)

	initialScore := engine.GetEffectiveScore("provider1")

	// Record an auth failure event
	engine.RecordEvent(ScoreEvent{
		Type:       EventHealthCheckAuthFail,
		Provider:   "provider1",
		Timestamp:  time.Now(),
		HTTPStatus: 401,
	})

	newScore := engine.GetEffectiveScore("provider1")

	expectedDrop := 50.0
	actualDrop := initialScore - newScore

	if actualDrop < expectedDrop-0.1 || actualDrop > expectedDrop+0.1 {
		t.Errorf("expected score to drop by ~%f, but dropped by %f", expectedDrop, actualDrop)
	}
}

func TestEngine_RecordEvent_Success(t *testing.T) {
	engine := NewEngine(
		WithOperationFailPenalty(5.0),
		WithSuccessBonus(0.5),
	)
	engine.RegisterProvider("provider1", 1)

	// First, create some penalty
	engine.RecordEvent(ScoreEvent{
		Type:     EventOperationFailed,
		Provider: "provider1",
	})

	scoreAfterFailure := engine.GetEffectiveScore("provider1")

	// Now record a success
	engine.RecordEvent(ScoreEvent{
		Type:     EventOperationSuccess,
		Provider: "provider1",
	})

	scoreAfterSuccess := engine.GetEffectiveScore("provider1")

	if scoreAfterSuccess <= scoreAfterFailure {
		t.Errorf("expected score to increase after success, got %f <= %f", scoreAfterSuccess, scoreAfterFailure)
	}
}

func TestEngine_MaxPenalty(t *testing.T) {
	engine := NewEngine(
		WithHealthCheckPenalty(30.0),
		WithMaxPenalty(50.0),
	)
	engine.RegisterProvider("provider1", 1)

	baseScore := engine.GetEffectiveScore("provider1")

	// Record many failures - should not exceed max penalty
	for i := 0; i < 10; i++ {
		engine.RecordEvent(ScoreEvent{
			Type:     EventHealthCheckFailed,
			Provider: "provider1",
		})
	}

	finalScore := engine.GetEffectiveScore("provider1")
	totalPenalty := baseScore - finalScore

	if totalPenalty > 50.0+0.1 {
		t.Errorf("penalty exceeded max penalty: got %f, max was 50.0", totalPenalty)
	}
}

func TestEngine_GetSortedProviders(t *testing.T) {
	engine := NewEngine(WithHealthCheckPenalty(30.0))
	engine.RegisterProvider("provider1", 1) // base: 100
	engine.RegisterProvider("provider2", 2) // base: 90
	engine.RegisterProvider("provider3", 3) // base: 80

	// Initially, providers should be sorted by base score
	sorted := engine.GetSortedProviders()
	if len(sorted) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(sorted))
	}
	if sorted[0] != "provider1" {
		t.Errorf("expected provider1 first, got %s", sorted[0])
	}

	// Penalize provider1 heavily
	engine.RecordEvent(ScoreEvent{
		Type:     EventHealthCheckFailed,
		Provider: "provider1",
	})
	engine.RecordEvent(ScoreEvent{
		Type:     EventHealthCheckFailed,
		Provider: "provider1",
	})

	// Now provider2 should be first (provider1 dropped below provider2)
	sorted = engine.GetSortedProviders()
	if sorted[0] != "provider2" {
		t.Errorf("expected provider2 first after penalties, got %s (scores: p1=%f, p2=%f)",
			sorted[0],
			engine.GetEffectiveScore("provider1"),
			engine.GetEffectiveScore("provider2"))
	}
}

func TestEngine_GetProviderStats(t *testing.T) {
	engine := NewEngine()
	engine.RegisterProvider("provider1", 1)

	// Record some events
	engine.RecordEvent(ScoreEvent{
		Type:         EventOperationSuccess,
		Provider:     "provider1",
		ResponseTime: 100 * time.Millisecond,
	})
	engine.RecordEvent(ScoreEvent{
		Type:     EventOperationFailed,
		Provider: "provider1",
	})

	stats := engine.GetProviderStats("provider1")
	if stats == nil {
		t.Fatal("expected stats to be returned")
	}

	if stats.TotalOperations != 2 {
		t.Errorf("expected 2 total operations, got %d", stats.TotalOperations)
	}
	if stats.SuccessfulOps != 1 {
		t.Errorf("expected 1 successful op, got %d", stats.SuccessfulOps)
	}
	if stats.FailedOps != 1 {
		t.Errorf("expected 1 failed op, got %d", stats.FailedOps)
	}
}

func TestEngine_StartStop(t *testing.T) {
	engine := NewEngine(WithDecayInterval(10 * time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine.Start(ctx)

	// Engine should be running
	// Let it run briefly
	time.Sleep(20 * time.Millisecond)

	engine.Stop()

	// Should be able to stop multiple times without panicking
	engine.Stop()
}

func TestEngine_Reset(t *testing.T) {
	engine := NewEngine(WithHealthCheckPenalty(20.0))
	engine.RegisterProvider("provider1", 1)

	initialScore := engine.GetEffectiveScore("provider1")

	// Add some penalties
	engine.RecordEvent(ScoreEvent{
		Type:     EventHealthCheckFailed,
		Provider: "provider1",
	})

	penalizedScore := engine.GetEffectiveScore("provider1")
	if penalizedScore >= initialScore {
		t.Error("expected score to decrease after failure")
	}

	// Reset
	engine.Reset()

	resetScore := engine.GetEffectiveScore("provider1")
	if resetScore != initialScore {
		t.Errorf("expected score to return to initial after reset, got %f, expected %f", resetScore, initialScore)
	}
}

func TestEngine_Disabled(t *testing.T) {
	engine := NewEngine(
		WithEnabled(false),
		WithHealthCheckPenalty(20.0),
	)
	engine.RegisterProvider("provider1", 1)

	initialScore := engine.GetEffectiveScore("provider1")

	// Record events (should be ignored)
	engine.RecordEvent(ScoreEvent{
		Type:     EventHealthCheckFailed,
		Provider: "provider1",
	})

	newScore := engine.GetEffectiveScore("provider1")
	if newScore != initialScore {
		t.Errorf("expected score to remain unchanged when disabled, got %f, expected %f", newScore, initialScore)
	}
}

func TestEngine_UnregisteredProvider(t *testing.T) {
	engine := NewEngine()

	// Recording events for unregistered provider should not panic
	engine.RecordEvent(ScoreEvent{
		Type:     EventHealthCheckFailed,
		Provider: "unknown_provider",
	})

	// Getting score for unregistered provider should return 0
	score := engine.GetEffectiveScore("unknown_provider")
	if score != 0 {
		t.Errorf("expected 0 for unregistered provider, got %f", score)
	}
}

func TestClassifyHealthCheckEvent(t *testing.T) {
	tests := []struct {
		name       string
		httpStatus int
		err        error
		wantType   ScoreEventType
	}{
		{
			name:       "429 rate limit",
			httpStatus: 429,
			wantType:   EventHealthCheck429,
		},
		{
			name:       "401 unauthorized",
			httpStatus: 401,
			wantType:   EventHealthCheckAuthFail,
		},
		{
			name:       "403 forbidden",
			httpStatus: 403,
			wantType:   EventHealthCheckAuthFail,
		},
		{
			name:       "500 server error",
			httpStatus: 500,
			wantType:   EventHealthCheckFailed,
		},
		{
			name:       "200 success",
			httpStatus: 200,
			wantType:   EventOperationSuccess,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := ClassifyHealthCheckEvent("test", tt.httpStatus, 100*time.Millisecond, tt.err)
			if event.Type != tt.wantType {
				t.Errorf("got event type %s, want %s", event.Type, tt.wantType)
			}
		})
	}
}

func TestClassifyOperationEvent(t *testing.T) {
	t.Run("successful operation", func(t *testing.T) {
		event := ClassifyOperationEvent("test", 100*time.Millisecond, nil)
		if event.Type != EventOperationSuccess {
			t.Errorf("got %s, want %s", event.Type, EventOperationSuccess)
		}
	})

	t.Run("failed operation", func(t *testing.T) {
		event := ClassifyOperationEvent("test", 100*time.Millisecond, testError("some error"))
		if event.Type != EventOperationFailed {
			t.Errorf("got %s, want %s", event.Type, EventOperationFailed)
		}
	})

	t.Run("rate limited operation", func(t *testing.T) {
		event := ClassifyOperationEvent("test", 100*time.Millisecond, testError("rate limit exceeded"))
		if event.Type != EventRateLimited {
			t.Errorf("got %s, want %s", event.Type, EventRateLimited)
		}
	})
}

// testError is a simple error implementation for testing
type testError string

func (e testError) Error() string {
	return string(e)
}
