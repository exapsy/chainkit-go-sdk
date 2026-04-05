package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestRedisStore_Name tests the store name identifier
func TestRedisStore_Name(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupRedisStore(t)
	defer cleanup()

	if store.Name() != "redis" {
		t.Errorf("expected name 'redis', got '%s'", store.Name())
	}
}

// TestRedisStore_SetAndGetScore tests basic set/get operations
func TestRedisStore_SetAndGetScore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupRedisStore(t)
	defer cleanup()

	ctx := context.Background()

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
		RecentLatencies:  []int64{1000000, 2000000, 3000000},
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

// TestRedisStore_GetNonExistent tests getting a non-existent score
func TestRedisStore_GetNonExistent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupRedisStore(t)
	defer cleanup()

	ctx := context.Background()

	retrieved, err := store.GetScore(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetScore should not error on non-existent provider: %v", err)
	}
	if retrieved != nil {
		t.Error("expected nil for non-existent provider")
	}
}

// TestRedisStore_UpdateScore tests updating an existing score
func TestRedisStore_UpdateScore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupRedisStore(t)
	defer cleanup()

	ctx := context.Background()

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

// TestRedisStore_GetAllScores tests retrieving all scores
func TestRedisStore_GetAllScores(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupRedisStore(t)
	defer cleanup()

	ctx := context.Background()

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

// TestRedisStore_DeleteScore tests deleting a score
func TestRedisStore_DeleteScore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupRedisStore(t)
	defer cleanup()

	ctx := context.Background()

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

// TestRedisStore_SetScores tests batch operations
func TestRedisStore_SetScores(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupRedisStore(t)
	defer cleanup()

	ctx := context.Background()

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
	}
}

// TestRedisStore_LatencyStats tests latency statistics operations
func TestRedisStore_LatencyStats(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupRedisStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create latency data
	latencyData := &LatencyStatsData{
		ProviderSamples: map[string][]int64{
			"provider1": {1000000, 2000000, 3000000},
			"provider2": {500000, 1000000, 1500000},
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
}

// TestRedisStore_TTL tests time-to-live functionality
func TestRedisStore_TTL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	config := RedisConfig{
		Addr:     getRedisAddr(t),
		ScoreTTL: 2 * time.Second, // Short TTL for testing
	}

	store, err := NewRedisStore(config)
	if err != nil {
		t.Fatalf("NewRedisStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	// Set score with TTL
	err = store.SetScore(ctx, data)
	if err != nil {
		t.Fatalf("SetScore failed: %v", err)
	}

	// Immediately verify it exists
	retrieved, err := store.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected score to exist immediately after set")
	}

	// Wait for TTL expiration
	time.Sleep(3 * time.Second)

	// Verify it's gone
	retrieved, err = store.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}
	if retrieved != nil {
		t.Error("expected score to be expired after TTL")
	}
}

// TestRedisStore_SetScoreWithTTL tests custom TTL per score
func TestRedisStore_SetScoreWithTTL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupRedisStore(t)
	defer cleanup()

	ctx := context.Background()

	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	// Set score with custom TTL
	err := store.SetScoreWithTTL(ctx, data, 2*time.Second)
	if err != nil {
		t.Fatalf("SetScoreWithTTL failed: %v", err)
	}

	// Verify it exists
	retrieved, err := store.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected score to exist")
	}

	// Wait for expiration
	time.Sleep(3 * time.Second)

	// Verify it's gone
	retrieved, err = store.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}
	if retrieved != nil {
		t.Error("expected score to be expired")
	}
}

// TestRedisStore_Watch tests pub/sub functionality
func TestRedisStore_Watch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupRedisStore(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Track received updates
	var mu sync.Mutex
	received := make(map[string]*ProviderScoreData)

	// Start watching in background
	watchDone := make(chan error, 1)
	go func() {
		err := store.Watch(ctx, func(name string, data *ProviderScoreData) {
			mu.Lock()
			received[name] = data
			mu.Unlock()
		})
		watchDone <- err
	}()

	// Give watch time to subscribe
	time.Sleep(100 * time.Millisecond)

	// Send some updates
	for i := 1; i <= 3; i++ {
		data := &ProviderScoreData{
			Name:      fmt.Sprintf("provider%d", i),
			BaseScore: float64(100 * i),
		}
		if err := store.SetScore(context.Background(), data); err != nil {
			t.Fatalf("SetScore failed: %v", err)
		}
	}

	// Wait a bit for pub/sub propagation
	time.Sleep(500 * time.Millisecond)

	// Verify we received updates
	mu.Lock()
	receivedCount := len(received)
	mu.Unlock()

	if receivedCount != 3 {
		t.Errorf("expected 3 updates, got %d", receivedCount)
	}

	// Cancel and verify watch returns
	cancel()
	err := <-watchDone
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestRedisStore_Lock tests distributed locking
func TestRedisStore_Lock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupRedisStore(t)
	defer cleanup()

	ctx := context.Background()

	// Acquire lock
	unlock, err := store.Lock(ctx, "provider1", 5*time.Second)
	if err != nil {
		t.Fatalf("Lock failed: %v", err)
	}

	// Try to acquire same lock (should fail)
	_, err = store.Lock(ctx, "provider1", 5*time.Second)
	if err == nil {
		t.Error("expected error when acquiring held lock")
	}

	// Release lock
	unlock()

	// Should be able to acquire now
	unlock2, err := store.Lock(ctx, "provider1", 5*time.Second)
	if err != nil {
		t.Fatalf("Lock after unlock failed: %v", err)
	}
	defer unlock2()
}

// TestRedisStore_LockExpiration tests lock TTL
func TestRedisStore_LockExpiration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupRedisStore(t)
	defer cleanup()

	ctx := context.Background()

	// Acquire lock with short TTL
	_, err := store.Lock(ctx, "provider1", 1*time.Second)
	if err != nil {
		t.Fatalf("Lock failed: %v", err)
	}

	// Don't unlock, wait for TTL expiration
	time.Sleep(2 * time.Second)

	// Should be able to acquire now
	unlock, err := store.Lock(ctx, "provider1", 5*time.Second)
	if err != nil {
		t.Fatalf("Lock after TTL expiration failed: %v", err)
	}
	defer unlock()
}

// TestRedisStore_ConcurrentAccess tests thread safety
func TestRedisStore_ConcurrentAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupRedisStore(t)
	defer cleanup()

	ctx := context.Background()

	const numGoroutines = 50
	const numOperations = 10

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			providerName := fmt.Sprintf("provider%d", id%5)

			for j := 0; j < numOperations; j++ {
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
	all, err := store.GetAllScores(ctx)
	if err != nil {
		t.Fatalf("GetAllScores failed after concurrent access: %v", err)
	}
	if len(all) == 0 {
		t.Error("expected some scores after concurrent writes")
	}
}

// TestRedisStore_Ping tests health check
func TestRedisStore_Ping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupRedisStore(t)
	defer cleanup()

	ctx := context.Background()

	err := store.Ping(ctx)
	if err != nil {
		t.Errorf("Ping failed: %v", err)
	}
}

// TestRedisStore_Close tests cleanup
func TestRedisStore_Close(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupRedisStore(t)
	defer cleanup()

	err := store.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// After close, operations should fail
	ctx := context.Background()
	_, err = store.GetScore(ctx, "provider1")
	if err == nil {
		t.Error("expected error after Close")
	}
}

// TestRedisStore_KeyPrefix tests custom key prefixing
func TestRedisStore_KeyPrefix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	config := RedisConfig{
		Addr:      getRedisAddr(t),
		KeyPrefix: "test:custom:",
	}

	store, err := NewRedisStore(config)
	if err != nil {
		t.Fatalf("NewRedisStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	err = store.SetScore(ctx, data)
	if err != nil {
		t.Fatalf("SetScore failed: %v", err)
	}

	// Verify the key was created with custom prefix
	expectedKey := "test:custom:score:provider1"
	exists, err := store.client.Exists(ctx, expectedKey).Result()
	if err != nil {
		t.Fatalf("Redis EXISTS failed: %v", err)
	}
	if exists != 1 {
		t.Errorf("expected key %s to exist", expectedKey)
	}
}

// Benchmarks

func BenchmarkRedisStore_SetScore(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping integration benchmark")
	}

	store, cleanup := setupRedisStore(b)
	defer cleanup()

	ctx := context.Background()
	data := &ProviderScoreData{
		Name:            "provider1",
		BaseScore:       100.0,
		RecentLatencies: []int64{1000000, 2000000},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.SetScore(ctx, data)
	}
}

func BenchmarkRedisStore_GetScore(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping integration benchmark")
	}

	store, cleanup := setupRedisStore(b)
	defer cleanup()

	ctx := context.Background()
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

func BenchmarkRedisStore_SetScores(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping integration benchmark")
	}

	store, cleanup := setupRedisStore(b)
	defer cleanup()

	ctx := context.Background()
	batch := make([]*ProviderScoreData, 10)
	for i := range batch {
		batch[i] = &ProviderScoreData{
			Name:      fmt.Sprintf("provider%d", i),
			BaseScore: 100.0,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.SetScores(ctx, batch)
	}
}

// Helper functions

func setupRedisStore(t testing.TB) (*RedisStore, func()) {
	config := RedisConfig{
		Addr: getRedisAddr(t),
	}

	store, err := NewRedisStore(config)
	if err != nil {
		t.Fatalf("NewRedisStore failed: %v", err)
	}

	cleanup := func() {
		// Clean up all test keys
		ctx := context.Background()
		pattern := store.keyPrefix + "*"
		iter := store.client.Scan(ctx, 0, pattern, 0).Iterator()
		for iter.Next(ctx) {
			store.client.Del(ctx, iter.Val())
		}
		store.Close()
	}

	return store, cleanup
}

func getRedisAddr(t testing.TB) string {
	// Check if running in CI or with existing Redis
	if addr := testcontainersRedisAddr; addr != "" {
		return addr
	}

	// Start Redis container for testing
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start Redis container: %v", err)
	}

	// Store container for cleanup
	t.Cleanup(func() {
		_ = container.Terminate(ctx)
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get container host: %v", err)
	}

	port, err := container.MappedPort(ctx, "6379")
	if err != nil {
		t.Fatalf("failed to get container port: %v", err)
	}

	addr := fmt.Sprintf("%s:%s", host, port.Port())
	testcontainersRedisAddr = addr
	return addr
}

// Global variable to reuse Redis container across tests
var testcontainersRedisAddr string
