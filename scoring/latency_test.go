package scoring

import (
	"testing"
	"time"
)

func TestNewLatencyTracker(t *testing.T) {
	t.Run("creates tracker with specified window size", func(t *testing.T) {
		tracker := NewLatencyTracker(50)
		if tracker == nil {
			t.Fatal("expected tracker to be created")
		}
		if tracker.windowSize != 50 {
			t.Errorf("expected window size 50, got %d", tracker.windowSize)
		}
	})

	t.Run("uses default window size for invalid input", func(t *testing.T) {
		tracker := NewLatencyTracker(0)
		if tracker.windowSize != 100 {
			t.Errorf("expected default window size 100, got %d", tracker.windowSize)
		}

		tracker = NewLatencyTracker(-10)
		if tracker.windowSize != 100 {
			t.Errorf("expected default window size 100 for negative input, got %d", tracker.windowSize)
		}
	})
}

func TestLatencyTracker_RecordLatency(t *testing.T) {
	tracker := NewLatencyTracker(5)

	// Record some latencies
	tracker.RecordLatency("provider1", 100*time.Millisecond)
	tracker.RecordLatency("provider1", 150*time.Millisecond)
	tracker.RecordLatency("provider2", 200*time.Millisecond)

	if tracker.GetProviderCount() != 2 {
		t.Errorf("expected 2 providers, got %d", tracker.GetProviderCount())
	}

	// Check that samples are recorded
	stats := tracker.GetProviderStats("provider1")
	if stats.SampleCount != 2 {
		t.Errorf("expected 2 samples for provider1, got %d", stats.SampleCount)
	}
}

func TestLatencyTracker_SlidingWindow(t *testing.T) {
	tracker := NewLatencyTracker(3) // Small window for testing

	// Fill the window
	tracker.RecordLatency("provider1", 100*time.Millisecond)
	tracker.RecordLatency("provider1", 200*time.Millisecond)
	tracker.RecordLatency("provider1", 300*time.Millisecond)

	// Add more - should evict oldest
	tracker.RecordLatency("provider1", 400*time.Millisecond)
	tracker.RecordLatency("provider1", 500*time.Millisecond)

	stats := tracker.GetProviderStats("provider1")
	if stats.SampleCount != 3 {
		t.Errorf("expected 3 samples (window size), got %d", stats.SampleCount)
	}

	// The oldest samples (100, 200) should be evicted
	// Remaining: 300, 400, 500 => mean = 400
	expectedMean := 400 * time.Millisecond
	if stats.Mean != expectedMean {
		t.Errorf("expected mean of %v, got %v", expectedMean, stats.Mean)
	}
}

func TestLatencyTracker_GetGlobalStats(t *testing.T) {
	tracker := NewLatencyTracker(100)

	// Record latencies for multiple providers
	tracker.RecordLatency("provider1", 100*time.Millisecond)
	tracker.RecordLatency("provider1", 200*time.Millisecond)
	tracker.RecordLatency("provider2", 300*time.Millisecond)
	tracker.RecordLatency("provider2", 400*time.Millisecond)

	stats := tracker.GetGlobalStats()

	if stats.SampleCount != 4 {
		t.Errorf("expected 4 total samples, got %d", stats.SampleCount)
	}

	// Mean should be (100+200+300+400)/4 = 250ms
	expectedMean := 250 * time.Millisecond
	if stats.Mean != expectedMean {
		t.Errorf("expected mean of %v, got %v", expectedMean, stats.Mean)
	}

	// Min should be 100ms
	if stats.Min != 100*time.Millisecond {
		t.Errorf("expected min of 100ms, got %v", stats.Min)
	}

	// Max should be 400ms
	if stats.Max != 400*time.Millisecond {
		t.Errorf("expected max of 400ms, got %v", stats.Max)
	}
}

func TestLatencyTracker_GetProviderSlownessFactor(t *testing.T) {
	tracker := NewLatencyTracker(100)

	// Create a scenario where one provider is clearly slower
	// Provider1: consistently fast (100ms)
	// Provider2: consistently slow (300ms)
	for i := 0; i < 10; i++ {
		tracker.RecordLatency("provider1", 100*time.Millisecond)
		tracker.RecordLatency("provider2", 300*time.Millisecond)
	}

	// Provider2 should have a positive slowness factor (slower than average)
	slowness2 := tracker.GetProviderSlownessFactor("provider2")
	if slowness2 <= 0 {
		t.Errorf("expected positive slowness factor for slow provider, got %f", slowness2)
	}

	// Provider1 should have a negative slowness factor (faster than average)
	slowness1 := tracker.GetProviderSlownessFactor("provider1")
	if slowness1 >= 0 {
		t.Errorf("expected negative slowness factor for fast provider, got %f", slowness1)
	}

	// Slowness of slow provider should be greater than fast provider
	if slowness2 <= slowness1 {
		t.Errorf("expected slowness2 (%f) > slowness1 (%f)", slowness2, slowness1)
	}
}

func TestLatencyTracker_GetProviderSlownessFactor_NoData(t *testing.T) {
	tracker := NewLatencyTracker(100)

	// No data recorded - should return 0
	slowness := tracker.GetProviderSlownessFactor("unknown")
	if slowness != 0 {
		t.Errorf("expected 0 for unknown provider, got %f", slowness)
	}
}

func TestLatencyTracker_GetProviderSlownessFactor_SingleProvider(t *testing.T) {
	tracker := NewLatencyTracker(100)

	// Only one provider - no variance, should return 0
	for i := 0; i < 10; i++ {
		tracker.RecordLatency("provider1", 100*time.Millisecond)
	}

	// With no variance, standard deviation is 0, so slowness should be 0
	slowness := tracker.GetProviderSlownessFactor("provider1")
	if slowness != 0 {
		t.Errorf("expected 0 for single provider with no variance, got %f", slowness)
	}
}

func TestLatencyTracker_Reset(t *testing.T) {
	tracker := NewLatencyTracker(100)

	tracker.RecordLatency("provider1", 100*time.Millisecond)
	tracker.RecordLatency("provider2", 200*time.Millisecond)

	if tracker.GetProviderCount() != 2 {
		t.Errorf("expected 2 providers before reset")
	}

	tracker.Reset()

	if tracker.GetProviderCount() != 0 {
		t.Errorf("expected 0 providers after reset, got %d", tracker.GetProviderCount())
	}

	stats := tracker.GetGlobalStats()
	if stats.SampleCount != 0 {
		t.Errorf("expected 0 samples after reset, got %d", stats.SampleCount)
	}
}

func TestLatencyTracker_Percentiles(t *testing.T) {
	tracker := NewLatencyTracker(100)

	// Record 100 latencies from 1ms to 100ms
	for i := 1; i <= 100; i++ {
		tracker.RecordLatency("provider1", time.Duration(i)*time.Millisecond)
	}

	stats := tracker.GetGlobalStats()

	// P50 should be around 50ms
	if stats.P50 < 45*time.Millisecond || stats.P50 > 55*time.Millisecond {
		t.Errorf("expected P50 around 50ms, got %v", stats.P50)
	}

	// P90 should be around 90ms
	if stats.P90 < 85*time.Millisecond || stats.P90 > 95*time.Millisecond {
		t.Errorf("expected P90 around 90ms, got %v", stats.P90)
	}

	// P99 should be around 99ms
	if stats.P99 < 95*time.Millisecond || stats.P99 > 100*time.Millisecond {
		t.Errorf("expected P99 around 99ms, got %v", stats.P99)
	}
}

func TestLatencyTracker_EmptyStats(t *testing.T) {
	tracker := NewLatencyTracker(100)

	stats := tracker.GetGlobalStats()

	if stats.SampleCount != 0 {
		t.Errorf("expected 0 samples, got %d", stats.SampleCount)
	}
	if stats.Mean != 0 {
		t.Errorf("expected 0 mean, got %v", stats.Mean)
	}

	providerStats := tracker.GetProviderStats("nonexistent")
	if providerStats.SampleCount != 0 {
		t.Errorf("expected 0 samples for nonexistent provider")
	}
}

func TestLatencyTracker_ConcurrentAccess(t *testing.T) {
	tracker := NewLatencyTracker(100)

	// Concurrent writes
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(providerNum int) {
			for j := 0; j < 100; j++ {
				providerName := "provider" + string(rune('A'+providerNum))
				tracker.RecordLatency(providerName, time.Duration(j)*time.Millisecond)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Concurrent reads while writing
	go func() {
		for i := 0; i < 100; i++ {
			tracker.RecordLatency("concurrent", time.Duration(i)*time.Millisecond)
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			tracker.GetGlobalStats()
			tracker.GetProviderSlownessFactor("concurrent")
		}
		done <- true
	}()

	<-done
	<-done

	// Should not panic and should have data
	if tracker.GetProviderCount() < 1 {
		t.Error("expected at least one provider after concurrent operations")
	}
}

func TestCalculateStats(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		stats := calculateStats([]time.Duration{})
		if stats.SampleCount != 0 {
			t.Errorf("expected 0 samples, got %d", stats.SampleCount)
		}
	})

	t.Run("single value", func(t *testing.T) {
		stats := calculateStats([]time.Duration{100 * time.Millisecond})
		if stats.Mean != 100*time.Millisecond {
			t.Errorf("expected mean of 100ms, got %v", stats.Mean)
		}
		if stats.StdDev != 0 {
			t.Errorf("expected stddev of 0, got %v", stats.StdDev)
		}
	})

	t.Run("multiple values", func(t *testing.T) {
		samples := []time.Duration{
			100 * time.Millisecond,
			200 * time.Millisecond,
			300 * time.Millisecond,
		}
		stats := calculateStats(samples)

		if stats.Mean != 200*time.Millisecond {
			t.Errorf("expected mean of 200ms, got %v", stats.Mean)
		}
		if stats.Min != 100*time.Millisecond {
			t.Errorf("expected min of 100ms, got %v", stats.Min)
		}
		if stats.Max != 300*time.Millisecond {
			t.Errorf("expected max of 300ms, got %v", stats.Max)
		}
	})
}

func TestPercentile(t *testing.T) {
	sorted := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
		50 * time.Millisecond,
	}

	t.Run("0th percentile", func(t *testing.T) {
		p := percentile(sorted, 0)
		if p != 10*time.Millisecond {
			t.Errorf("expected 10ms, got %v", p)
		}
	})

	t.Run("100th percentile", func(t *testing.T) {
		p := percentile(sorted, 100)
		if p != 50*time.Millisecond {
			t.Errorf("expected 50ms, got %v", p)
		}
	})

	t.Run("50th percentile", func(t *testing.T) {
		p := percentile(sorted, 50)
		if p != 30*time.Millisecond {
			t.Errorf("expected 30ms, got %v", p)
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		p := percentile([]time.Duration{}, 50)
		if p != 0 {
			t.Errorf("expected 0 for empty slice, got %v", p)
		}
	})
}
