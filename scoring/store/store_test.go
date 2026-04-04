package store

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestMemoryStore_Name tests the store name identifier
func TestMemoryStore_Name(t *testing.T) {
	store := NewMemoryStore()
	if store.Name() != "memory" {
		t.Errorf("expected name 'memory', got '%s'", store.Name())
	}
}

// TestMemoryStore_SetAndGetScore tests basic set/get operations
func TestMemoryStore_SetAndGetScore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Create test data
	data := &ProviderScoreData{
		Name:             "provider1",
		BaseScore:        100.0,
		HealthPenalty:    5.0,
		LatencyPenalty:   2.0,
		ErrorPenalty:     3.0,
		RateLimitPenalty: 1.0,
		TotalOperations:  100,
		SuccessfulOps:    95,
		FailedOps:        5,
		LastUpdated:      time.Now(),
		RecentLatencies:  []int64{1000000, 2000000, 3000000}, // 1ms, 2ms, 3ms in nanoseconds
	}

	// Set the score
	err := store.SetScore(ctx, data)
	if err != nil {
		t.Fatalf("SetScore failed: %v", err)
	}

	// Get the score back
	retrieved, err := store.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected non-nil score data")
	}

	// Verify all fields
	if retrieved.Name != data.Name {
		t.Errorf("Name mismatch: got %s, want %s", retrieved.Name, data.Name)
	}
	if retrieved.BaseScore != data.BaseScore {
		t.Errorf("BaseScore mismatch: got %f, want %f", retrieved.BaseScore, data.BaseScore)
	}
	if retrieved.HealthPenalty != data.HealthPenalty {
		t.Errorf("HealthPenalty mismatch: got %f, want %f", retrieved.HealthPenalty, data.HealthPenalty)
	}
	if retrieved.TotalOperations != data.TotalOperations {
		t.Errorf("TotalOperations mismatch: got %d, want %d", retrieved.TotalOperations, data.TotalOperations)
	}
	if len(retrieved.RecentLatencies) != len(data.RecentLatencies) {
		t.Errorf("RecentLatencies length mismatch: got %d, want %d", len(retrieved.RecentLatencies), len(data.RecentLatencies))
	}
}

// TestMemoryStore_GetNonExistent tests getting a non-existent score
func TestMemoryStore_GetNonExistent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	retrieved, err := store.GetScore(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetScore should not error on non-existent provider: %v", err)
	}
	if retrieved != nil {
		t.Error("expected nil for non-existent provider")
	}
}

// TestMemoryStore_UpdateScore tests updating an existing score
func TestMemoryStore_UpdateScore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Initial data
	data := &ProviderScoreData{
		Name:            "provider1",
		BaseScore:       100.0,
		HealthPenalty:   5.0,
		TotalOperations: 100,
	}

	err := store.SetScore(ctx, data)
	if err != nil {
		t.Fatalf("SetScore failed: %v", err)
	}

	// Update data
	data.HealthPenalty = 10.0
	data.TotalOperations = 200

	err = store.SetScore(ctx, data)
	if err != nil {
		t.Fatalf("SetScore (update) failed: %v", err)
	}

	// Verify update
	retrieved, err := store.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}

	if retrieved.HealthPenalty != 10.0 {
		t.Errorf("HealthPenalty not updated: got %f, want 10.0", retrieved.HealthPenalty)
	}
	if retrieved.TotalOperations != 200 {
		t.Errorf("TotalOperations not updated: got %d, want 200", retrieved.TotalOperations)
	}
}

// TestMemoryStore_GetAllScores tests retrieving all scores
func TestMemoryStore_GetAllScores(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Add multiple providers
	providers := []string{"provider1", "provider2", "provider3"}
	for _, name := range providers {
		data := &ProviderScoreData{
			Name:      name,
			BaseScore: 100.0,
		}
		err := store.SetScore(ctx, data)
		if err != nil {
			t.Fatalf("SetScore failed for %s: %v", name, err)
		}
	}

	// Get all scores
	all, err := store.GetAllScores(ctx)
	if err != nil {
		t.Fatalf("GetAllScores failed: %v", err)
	}

	if len(all) != len(providers) {
		t.Errorf("expected %d scores, got %d", len(providers), len(all))
	}

	// Verify all providers are present
	found := make(map[string]bool)
	for _, data := range all {
		found[data.Name] = true
	}

	for _, name := range providers {
		if !found[name] {
			t.Errorf("provider %s not found in GetAllScores", name)
		}
	}
}

// TestMemoryStore_DeleteScore tests deleting a score
func TestMemoryStore_DeleteScore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Add a score
	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}
	err := store.SetScore(ctx, data)
	if err != nil {
		t.Fatalf("SetScore failed: %v", err)
	}

	// Delete it
	err = store.DeleteScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("DeleteScore failed: %v", err)
	}

	// Verify it's gone
	retrieved, err := store.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}
	if retrieved != nil {
		t.Error("expected nil after deletion")
	}
}

// TestMemoryStore_SetScores tests batch operations
func TestMemoryStore_SetScores(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Create batch data
	batch := []*ProviderScoreData{
		{Name: "provider1", BaseScore: 100.0, HealthPenalty: 1.0},
		{Name: "provider2", BaseScore: 90.0, HealthPenalty: 2.0},
		{Name: "provider3", BaseScore: 80.0, HealthPenalty: 3.0},
	}

	// Set all at once
	err := store.SetScores(ctx, batch)
	if err != nil {
		t.Fatalf("SetScores failed: %v", err)
	}

	// Verify all are present
	for _, expected := range batch {
		retrieved, err := store.GetScore(ctx, expected.Name)
		if err != nil {
			t.Fatalf("GetScore failed for %s: %v", expected.Name, err)
		}
		if retrieved == nil {
			t.Fatalf("expected non-nil score for %s", expected.Name)
		}
		if retrieved.BaseScore != expected.BaseScore {
			t.Errorf("BaseScore mismatch for %s: got %f, want %f",
				expected.Name, retrieved.BaseScore, expected.BaseScore)
		}
		if retrieved.HealthPenalty != expected.HealthPenalty {
			t.Errorf("HealthPenalty mismatch for %s: got %f, want %f",
				expected.Name, retrieved.HealthPenalty, expected.HealthPenalty)
		}
	}
}

// TestMemoryStore_LatencyStats tests latency statistics operations
func TestMemoryStore_LatencyStats(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Create latency data
	latencyData := &LatencyStatsData{
		ProviderSamples: map[string][]int64{
			"provider1": {1000000, 2000000, 3000000}, // 1ms, 2ms, 3ms
			"provider2": {500000, 1000000, 1500000},  // 0.5ms, 1ms, 1.5ms
		},
		LastUpdated: time.Now(),
	}

	// Set latency stats
	err := store.SetLatencyStats(ctx, latencyData)
	if err != nil {
		t.Fatalf("SetLatencyStats failed: %v", err)
	}

	// Get latency stats back
	retrieved, err := store.GetLatencyStats(ctx)
	if err != nil {
		t.Fatalf("GetLatencyStats failed: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected non-nil latency data")
	}

	if len(retrieved.ProviderSamples) != len(latencyData.ProviderSamples) {
		t.Errorf("ProviderSamples length mismatch: got %d, want %d",
			len(retrieved.ProviderSamples), len(latencyData.ProviderSamples))
	}

	// Verify provider samples
	for provider, samples := range latencyData.ProviderSamples {
		retrievedSamples, ok := retrieved.ProviderSamples[provider]
		if !ok {
			t.Errorf("provider %s not found in retrieved latency data", provider)
			continue
		}

		if len(retrievedSamples) != len(samples) {
			t.Errorf("sample count mismatch for %s: got %d, want %d",
				provider, len(retrievedSamples), len(samples))
		}
	}
}

// TestMemoryStore_LatencyStatsEmpty tests getting latency stats when none exist
func TestMemoryStore_LatencyStatsEmpty(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	retrieved, err := store.GetLatencyStats(ctx)
	if err != nil {
		t.Fatalf("GetLatencyStats should not error when empty: %v", err)
	}
	if retrieved != nil {
		t.Error("expected nil for non-existent latency stats")
	}
}

// TestMemoryStore_Isolation tests data isolation (defensive copying)
func TestMemoryStore_Isolation(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Create and set data
	data := &ProviderScoreData{
		Name:            "provider1",
		BaseScore:       100.0,
		HealthPenalty:   5.0,
		RecentLatencies: []int64{1000000, 2000000},
	}

	err := store.SetScore(ctx, data)
	if err != nil {
		t.Fatalf("SetScore failed: %v", err)
	}

	// Modify original data
	data.HealthPenalty = 50.0
	data.RecentLatencies[0] = 9999999

	// Get data back
	retrieved, err := store.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}

	// Verify the stored data was not affected by external modifications
	if retrieved.HealthPenalty != 5.0 {
		t.Errorf("stored data was modified: HealthPenalty got %f, want 5.0", retrieved.HealthPenalty)
	}
	if retrieved.RecentLatencies[0] != 1000000 {
		t.Errorf("stored data was modified: RecentLatencies[0] got %d, want 1000000",
			retrieved.RecentLatencies[0])
	}

	// Modify retrieved data
	retrieved.BaseScore = 999.0
	if len(retrieved.RecentLatencies) > 0 {
		retrieved.RecentLatencies[0] = 8888888
	}

	// Get data again
	retrieved2, err := store.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore (2nd) failed: %v", err)
	}

	// Verify stored data still unchanged
	if retrieved2.BaseScore != 100.0 {
		t.Errorf("stored data was modified: BaseScore got %f, want 100.0", retrieved2.BaseScore)
	}
	if len(retrieved2.RecentLatencies) > 0 && retrieved2.RecentLatencies[0] != 1000000 {
		t.Errorf("stored data was modified: RecentLatencies[0] got %d, want 1000000",
			retrieved2.RecentLatencies[0])
	}
}

// TestMemoryStore_Concurrency tests concurrent access safety
func TestMemoryStore_Concurrency(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	const numGoroutines = 100
	const numOperations = 10

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Run concurrent operations
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			providerName := "provider1"

			for j := 0; j < numOperations; j++ {
				// Mix of reads and writes
				if j%2 == 0 {
					// Write
					data := &ProviderScoreData{
						Name:            providerName,
						BaseScore:       float64(100 + id),
						TotalOperations: int64(j),
					}
					_ = store.SetScore(ctx, data)
				} else {
					// Read
					_, _ = store.GetScore(ctx, providerName)
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify store is still functional
	data, err := store.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore failed after concurrent access: %v", err)
	}
	if data == nil {
		t.Error("expected data after concurrent writes")
	}
}

// TestMemoryStore_NilHandling tests handling of nil data
func TestMemoryStore_NilHandling(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// SetScore with nil should not error
	err := store.SetScore(ctx, nil)
	if err != nil {
		t.Errorf("SetScore with nil should not error: %v", err)
	}

	// SetScores with nil elements
	err = store.SetScores(ctx, []*ProviderScoreData{
		{Name: "valid", BaseScore: 100.0},
		nil,
		{Name: "valid2", BaseScore: 90.0},
	})
	if err != nil {
		t.Errorf("SetScores with nil elements should not error: %v", err)
	}

	// Verify valid scores were set
	data, err := store.GetScore(ctx, "valid")
	if err != nil || data == nil {
		t.Error("expected valid score to be set")
	}

	// SetLatencyStats with nil should not error
	err = store.SetLatencyStats(ctx, nil)
	if err != nil {
		t.Errorf("SetLatencyStats with nil should not error: %v", err)
	}
}

// TestMemoryStore_EmptyName tests handling of empty provider names
func TestMemoryStore_EmptyName(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// SetScore with empty name should not store
	data := &ProviderScoreData{
		Name:      "",
		BaseScore: 100.0,
	}

	err := store.SetScore(ctx, data)
	if err != nil {
		t.Fatalf("SetScore should not error: %v", err)
	}

	// GetAllScores should return empty
	all, err := store.GetAllScores(ctx)
	if err != nil {
		t.Fatalf("GetAllScores failed: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected 0 scores with empty name, got %d", len(all))
	}
}

// TestMemoryStore_CloseAndPing tests lifecycle methods
func TestMemoryStore_CloseAndPing(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Ping should always succeed
	err := store.Ping(ctx)
	if err != nil {
		t.Errorf("Ping failed: %v", err)
	}

	// Close should succeed
	err = store.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Store should still work after close (memory store doesn't need cleanup)
	err = store.Ping(ctx)
	if err != nil {
		t.Errorf("Ping after Close failed: %v", err)
	}
}

// TestStoreRegistry tests the store factory registration
func TestStoreRegistry(t *testing.T) {
	// Memory store should be registered by default
	config := StoreConfig{Type: StoreTypeMemory}

	store, err := NewStore(config)
	if err != nil {
		t.Fatalf("NewStore failed for memory type: %v", err)
	}

	if store == nil {
		t.Fatal("expected non-nil store")
	}

	if store.Name() != "memory" {
		t.Errorf("expected memory store, got %s", store.Name())
	}
}

// TestStoreRegistry_UnknownType tests handling of unknown store types
func TestStoreRegistry_UnknownType(t *testing.T) {
	config := StoreConfig{Type: "unknown"}

	store, err := NewStore(config)
	if err == nil {
		t.Error("expected error for unknown store type")
	}
	if store != nil {
		t.Error("expected nil store for unknown type")
	}
}

// TestStoreRegistry_CustomFactory tests registering a custom factory
func TestStoreRegistry_CustomFactory(t *testing.T) {
	customType := StoreType("custom")

	// Register custom factory
	Register(customType, func(config StoreConfig) (ScoreStore, error) {
		return NewMemoryStore(), nil
	})

	// Use custom factory
	config := StoreConfig{Type: customType}
	store, err := NewStore(config)
	if err != nil {
		t.Fatalf("NewStore failed for custom type: %v", err)
	}

	if store == nil {
		t.Fatal("expected non-nil store from custom factory")
	}
}

// Benchmarks

// BenchmarkMemoryStore_SetScore benchmarks single score writes
func BenchmarkMemoryStore_SetScore(b *testing.B) {
	ctx := context.Background()
	store := NewMemoryStore()

	data := &ProviderScoreData{
		Name:            "provider1",
		BaseScore:       100.0,
		HealthPenalty:   5.0,
		RecentLatencies: []int64{1000000, 2000000, 3000000},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.SetScore(ctx, data)
	}
}

// BenchmarkMemoryStore_GetScore benchmarks single score reads
func BenchmarkMemoryStore_GetScore(b *testing.B) {
	ctx := context.Background()
	store := NewMemoryStore()

	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}
	_ = store.SetScore(ctx, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.GetScore(ctx, "provider1")
	}
}

// BenchmarkMemoryStore_SetScores benchmarks batch writes
func BenchmarkMemoryStore_SetScores(b *testing.B) {
	ctx := context.Background()
	store := NewMemoryStore()

	batch := make([]*ProviderScoreData, 10)
	for i := range batch {
		batch[i] = &ProviderScoreData{
			Name:      "provider" + string(rune(i)),
			BaseScore: 100.0,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.SetScores(ctx, batch)
	}
}

// BenchmarkMemoryStore_GetAllScores benchmarks getting all scores
func BenchmarkMemoryStore_GetAllScores(b *testing.B) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Pre-populate with 100 providers
	for i := 0; i < 100; i++ {
		data := &ProviderScoreData{
			Name:      "provider" + string(rune(i)),
			BaseScore: 100.0,
		}
		_ = store.SetScore(ctx, data)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.GetAllScores(ctx)
	}
}

// BenchmarkMemoryStore_ConcurrentReadWrite benchmarks concurrent access
func BenchmarkMemoryStore_ConcurrentReadWrite(b *testing.B) {
	ctx := context.Background()
	store := NewMemoryStore()

	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				_ = store.SetScore(ctx, data)
			} else {
				_, _ = store.GetScore(ctx, "provider1")
			}
			i++
		}
	})
}
