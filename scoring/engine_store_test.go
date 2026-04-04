package scoring

import (
	"context"
	"testing"
	"time"

	"github.com/exapsy/chainkit/scoring/store"
)

// TestEngine_WithMemoryStore tests engine with explicitly configured memory store
func TestEngine_WithMemoryStore(t *testing.T) {
	engine := NewEngine(WithMemoryStore())

	if engine.GetStore() == nil {
		t.Fatal("expected store to be set")
	}

	if engine.GetStore().Name() != "memory" {
		t.Errorf("expected memory store, got %s", engine.GetStore().Name())
	}
}

// TestEngine_WithoutStore tests engine works without a store (backwards compatibility)
func TestEngine_WithoutStore(t *testing.T) {
	engine := NewEngine()

	// Engine should work without a store
	engine.RegisterProvider("provider1", 1)
	engine.RegisterProvider("provider2", 2)

	// Record some events
	engine.RecordEvent(ScoreEvent{
		Provider:     "provider1",
		Type:         EventOperationSuccess,
		ResponseTime: 100 * time.Millisecond,
	})

	// Get scores (should work)
	score := engine.GetEffectiveScore("provider1")
	if score <= 0 {
		t.Errorf("expected positive score, got %f", score)
	}
}

// TestEngine_LoadFromStore tests loading scores from a store
func TestEngine_LoadFromStore(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	// Pre-populate store with score data
	scoreData := &store.ProviderScoreData{
		Name:             "provider1",
		BaseScore:        100.0,
		HealthPenalty:    5.0,
		LatencyPenalty:   2.0,
		ErrorPenalty:     3.0,
		RateLimitPenalty: 1.0,
		TotalOperations:  50,
		SuccessfulOps:    45,
		FailedOps:        5,
		LastUpdated:      time.Now(),
		RecentLatencies:  []int64{1000000, 2000000, 3000000}, // 1ms, 2ms, 3ms
	}

	err := memStore.SetScore(ctx, scoreData)
	if err != nil {
		t.Fatalf("failed to pre-populate store: %v", err)
	}

	// Create engine with store
	engine := NewEngine(WithStore(memStore))

	// Verify provider was loaded
	if !engine.HasProvider("provider1") {
		t.Fatal("provider1 should be loaded from store")
	}

	// Verify score components
	stats := engine.GetProviderStats("provider1")
	if stats == nil {
		t.Fatal("expected non-nil stats")
	}

	if stats.BaseScore != 100.0 {
		t.Errorf("BaseScore: got %f, want 100.0", stats.BaseScore)
	}
	if stats.HealthPenalty != 5.0 {
		t.Errorf("HealthPenalty: got %f, want 5.0", stats.HealthPenalty)
	}
	if stats.TotalOperations != 50 {
		t.Errorf("TotalOperations: got %d, want 50", stats.TotalOperations)
	}
}

// TestEngine_SaveToStore tests saving all scores to a store
func TestEngine_SaveToStore(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	// Create engine with store
	engine := NewEngine(WithStore(memStore))

	// Register providers and record events
	engine.RegisterProvider("provider1", 1)
	engine.RegisterProvider("provider2", 2)

	engine.RecordEvent(ScoreEvent{
		Provider:     "provider1",
		Type:         EventOperationSuccess,
		ResponseTime: 100 * time.Millisecond,
	})

	engine.RecordEvent(ScoreEvent{
		Provider: "provider2",
		Type:     EventHealthCheckFailed,
	})

	// Save to store
	err := engine.SaveToStore(ctx)
	if err != nil {
		t.Fatalf("SaveToStore failed: %v", err)
	}

	// Verify data in store
	data1, err := memStore.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}
	if data1 == nil {
		t.Fatal("provider1 not found in store")
	}

	data2, err := memStore.GetScore(ctx, "provider2")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}
	if data2 == nil {
		t.Fatal("provider2 not found in store")
	}

	// Verify provider2 has health penalty
	if data2.HealthPenalty <= 0 {
		t.Error("expected provider2 to have health penalty")
	}
}

// TestEngine_PersistScore tests persisting a single provider's score
func TestEngine_PersistScore(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	engine := NewEngine(WithStore(memStore))
	engine.RegisterProvider("provider1", 1)
	engine.RegisterProvider("provider2", 2)

	// Record event for provider1
	engine.RecordEvent(ScoreEvent{
		Provider: "provider1",
		Type:     EventOperationFailed,
	})

	// Persist only provider1
	err := engine.PersistScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("PersistScore failed: %v", err)
	}

	// Verify provider1 is in store
	data1, err := memStore.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}
	if data1 == nil {
		t.Fatal("provider1 not found in store")
	}
	if data1.ErrorPenalty <= 0 {
		t.Error("expected provider1 to have error penalty")
	}

	// Verify provider2 is NOT in store (not persisted)
	data2, err := memStore.GetScore(ctx, "provider2")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}
	if data2 != nil {
		t.Error("provider2 should not be in store")
	}
}

// TestEngine_PersistScore_NonExistent tests persisting a non-existent provider
func TestEngine_PersistScore_NonExistent(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	engine := NewEngine(WithStore(memStore))

	err := engine.PersistScore(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error when persisting non-existent provider")
	}
}

// TestEngine_RoundTrip tests saving and loading scores
func TestEngine_RoundTrip(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	// Create first engine, register providers, record events
	engine1 := NewEngine(WithStore(memStore))
	engine1.RegisterProvider("provider1", 1)
	engine1.RegisterProvider("provider2", 2)
	engine1.RegisterProvider("provider3", 3)

	engine1.RecordEvent(ScoreEvent{
		Provider:     "provider1",
		Type:         EventOperationSuccess,
		ResponseTime: 50 * time.Millisecond,
	})

	engine1.RecordEvent(ScoreEvent{
		Provider: "provider2",
		Type:     EventRateLimited,
	})

	engine1.RecordEvent(ScoreEvent{
		Provider: "provider3",
		Type:     EventHealthCheckFailed,
	})

	// Save to store
	err := engine1.SaveToStore(ctx)
	if err != nil {
		t.Fatalf("SaveToStore failed: %v", err)
	}

	// Get stats from engine1
	stats1 := engine1.GetProviderStats("provider1")
	stats2 := engine1.GetProviderStats("provider2")
	stats3 := engine1.GetProviderStats("provider3")

	// Create second engine with same store (simulates restart)
	engine2 := NewEngine(WithStore(memStore))

	// Verify providers were loaded
	if engine2.GetProviderCount() != 3 {
		t.Errorf("expected 3 providers, got %d", engine2.GetProviderCount())
	}

	// Verify stats match
	newStats1 := engine2.GetProviderStats("provider1")
	if newStats1 == nil {
		t.Fatal("provider1 not loaded")
	}
	if newStats1.BaseScore != stats1.BaseScore {
		t.Errorf("BaseScore mismatch for provider1: got %f, want %f",
			newStats1.BaseScore, stats1.BaseScore)
	}

	newStats2 := engine2.GetProviderStats("provider2")
	if newStats2 == nil {
		t.Fatal("provider2 not loaded")
	}
	if newStats2.RateLimitPenalty != stats2.RateLimitPenalty {
		t.Errorf("RateLimitPenalty mismatch for provider2: got %f, want %f",
			newStats2.RateLimitPenalty, stats2.RateLimitPenalty)
	}

	newStats3 := engine2.GetProviderStats("provider3")
	if newStats3 == nil {
		t.Fatal("provider3 not loaded")
	}
	if newStats3.HealthPenalty != stats3.HealthPenalty {
		t.Errorf("HealthPenalty mismatch for provider3: got %f, want %f",
			newStats3.HealthPenalty, stats3.HealthPenalty)
	}
}

// TestEngine_SetStore tests changing the store at runtime
func TestEngine_SetStore(t *testing.T) {
	ctx := context.Background()

	// Create engine without store
	engine := NewEngine()
	engine.RegisterProvider("provider1", 1)

	if engine.GetStore() != nil {
		t.Error("expected no store initially")
	}

	// Set a store
	memStore := store.NewMemoryStore()
	err := engine.SetStore(memStore)
	if err != nil {
		t.Fatalf("SetStore failed: %v", err)
	}

	if engine.GetStore() == nil {
		t.Fatal("expected store to be set")
	}

	// Save scores
	err = engine.SaveToStore(ctx)
	if err != nil {
		t.Fatalf("SaveToStore failed: %v", err)
	}

	// Verify in store
	data, err := memStore.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}
	if data == nil {
		t.Fatal("provider1 not in store")
	}
}

// TestEngine_LatencyTrackerPersistence tests latency statistics persistence
func TestEngine_LatencyTrackerPersistence(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	// Create engine and record latencies
	engine1 := NewEngine(WithStore(memStore))
	engine1.RegisterProvider("provider1", 1)
	engine1.RegisterProvider("provider2", 2)

	engine1.RecordEvent(ScoreEvent{
		Provider:     "provider1",
		Type:         EventOperationSuccess,
		ResponseTime: 100 * time.Millisecond,
	})

	engine1.RecordEvent(ScoreEvent{
		Provider:     "provider2",
		Type:         EventOperationSuccess,
		ResponseTime: 200 * time.Millisecond,
	})

	// Save to store
	err := engine1.SaveToStore(ctx)
	if err != nil {
		t.Fatalf("SaveToStore failed: %v", err)
	}

	// Verify latency stats are in store
	latencyData, err := memStore.GetLatencyStats(ctx)
	if err != nil {
		t.Fatalf("GetLatencyStats failed: %v", err)
	}
	if latencyData == nil {
		t.Fatal("expected latency data in store")
	}

	// Create new engine (simulates restart)
	engine2 := NewEngine(WithStore(memStore))

	// Verify latency stats were loaded
	stats := engine2.GetLatencyStats()
	if stats == nil {
		t.Fatal("latency stats not loaded")
	}
	if stats.SampleCount <= 0 {
		t.Error("expected latency samples to be loaded")
	}
}

// TestEngine_WithStoreConfig tests WithStoreConfig option
func TestEngine_WithStoreConfig(t *testing.T) {
	config := store.StoreConfig{
		Type: store.StoreTypeMemory,
	}

	engine := NewEngine(WithStoreConfig(config))

	if engine.GetStore() == nil {
		t.Fatal("expected store to be set from config")
	}

	if engine.GetStore().Name() != "memory" {
		t.Errorf("expected memory store, got %s", engine.GetStore().Name())
	}
}

// TestEngine_StoreErrorHandling tests error handling when store operations fail
func TestEngine_StoreErrorHandling(t *testing.T) {
	ctx := context.Background()

	// Engine without store should not error
	engine := NewEngine()
	engine.RegisterProvider("provider1", 1)

	err := engine.SaveToStore(ctx)
	if err != nil {
		t.Errorf("SaveToStore without store should not error: %v", err)
	}

	err = engine.LoadFromStore(ctx)
	if err != nil {
		t.Errorf("LoadFromStore without store should not error: %v", err)
	}

	err = engine.PersistScore(ctx, "provider1")
	if err != nil {
		t.Errorf("PersistScore without store should not error: %v", err)
	}
}

// TestEngine_LoadFromStore_EmptyStore tests loading from an empty store
func TestEngine_LoadFromStore_EmptyStore(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	engine := NewEngine(WithStore(memStore))

	// Should not error with empty store
	err := engine.LoadFromStore(ctx)
	if err != nil {
		t.Errorf("LoadFromStore from empty store should not error: %v", err)
	}

	// Engine should have no providers
	if engine.GetProviderCount() != 0 {
		t.Errorf("expected 0 providers, got %d", engine.GetProviderCount())
	}
}

// TestEngine_ConversionRoundTrip tests conversion between ProviderScore and ProviderScoreData
func TestEngine_ConversionRoundTrip(t *testing.T) {
	// Create a ProviderScore
	ps := NewProviderScore("test-provider", 1, 100)
	ps.HealthPenalty = 5.0
	ps.LatencyPenalty = 2.0
	ps.ErrorPenalty = 3.0
	ps.RateLimitPenalty = 1.0
	ps.TotalOperations = 100
	ps.SuccessfulOps = 95
	ps.FailedOps = 5
	ps.RecordLatency(50 * time.Millisecond)
	ps.RecordLatency(100 * time.Millisecond)

	// Convert to store data
	data := ToStoreData(ps)
	if data == nil {
		t.Fatal("ToStoreData returned nil")
	}

	// Verify conversion
	if data.Name != ps.Name {
		t.Errorf("Name mismatch: got %s, want %s", data.Name, ps.Name)
	}
	if data.BaseScore != ps.BaseScore {
		t.Errorf("BaseScore mismatch: got %f, want %f", data.BaseScore, ps.BaseScore)
	}
	if data.HealthPenalty != ps.HealthPenalty {
		t.Errorf("HealthPenalty mismatch: got %f, want %f", data.HealthPenalty, ps.HealthPenalty)
	}
	if len(data.RecentLatencies) != len(ps.RecentLatencies) {
		t.Errorf("RecentLatencies length mismatch: got %d, want %d",
			len(data.RecentLatencies), len(ps.RecentLatencies))
	}

	// Convert back to ProviderScore
	ps2 := FromStoreData(data, 100)
	if ps2 == nil {
		t.Fatal("FromStoreData returned nil")
	}

	// Verify round-trip
	if ps2.Name != ps.Name {
		t.Errorf("Name mismatch after round-trip: got %s, want %s", ps2.Name, ps.Name)
	}
	if ps2.BaseScore != ps.BaseScore {
		t.Errorf("BaseScore mismatch after round-trip: got %f, want %f", ps2.BaseScore, ps.BaseScore)
	}
	if ps2.TotalOperations != ps.TotalOperations {
		t.Errorf("TotalOperations mismatch after round-trip: got %d, want %d",
			ps2.TotalOperations, ps.TotalOperations)
	}
}

// TestEngine_MultipleProvidersPersistence tests persistence with many providers
func TestEngine_MultipleProvidersPersistence(t *testing.T) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	engine := NewEngine(WithStore(memStore))

	// Register many providers
	const numProviders = 50
	for i := 1; i <= numProviders; i++ {
		name := "provider" + string(rune(i))
		engine.RegisterProvider(name, i)
	}

	// Save all
	err := engine.SaveToStore(ctx)
	if err != nil {
		t.Fatalf("SaveToStore failed: %v", err)
	}

	// Verify all in store
	allScores, err := memStore.GetAllScores(ctx)
	if err != nil {
		t.Fatalf("GetAllScores failed: %v", err)
	}

	if len(allScores) != numProviders {
		t.Errorf("expected %d scores in store, got %d", numProviders, len(allScores))
	}

	// Create new engine and load
	engine2 := NewEngine(WithStore(memStore))

	if engine2.GetProviderCount() != numProviders {
		t.Errorf("expected %d providers loaded, got %d", numProviders, engine2.GetProviderCount())
	}
}

// BenchmarkEngine_SaveToStore benchmarks saving all scores
func BenchmarkEngine_SaveToStore(b *testing.B) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	engine := NewEngine(WithStore(memStore))
	for i := 0; i < 100; i++ {
		engine.RegisterProvider("provider"+string(rune(i)), i+1)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = engine.SaveToStore(ctx)
	}
}

// BenchmarkEngine_LoadFromStore benchmarks loading all scores
func BenchmarkEngine_LoadFromStore(b *testing.B) {
	ctx := context.Background()
	memStore := store.NewMemoryStore()

	// Pre-populate store
	engine := NewEngine(WithStore(memStore))
	for i := 0; i < 100; i++ {
		engine.RegisterProvider("provider"+string(rune(i)), i+1)
	}
	_ = engine.SaveToStore(ctx)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine2 := NewEngine(WithStore(memStore))
		_ = engine2.LoadFromStore(ctx)
	}
}
