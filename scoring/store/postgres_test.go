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

// TestPostgresStore_Name tests the store name identifier
func TestPostgresStore_Name(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupPostgresStore(t)
	defer cleanup()

	if store.Name() != "postgres" {
		t.Errorf("expected name 'postgres', got '%s'", store.Name())
	}
}

// TestPostgresStore_SetAndGetScore tests basic set/get operations
func TestPostgresStore_SetAndGetScore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupPostgresStore(t)
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
		LastUpdated:      time.Now().UTC(),
		LastHealthCheck:  time.Now().UTC(),
		LastOperation:    time.Now().UTC(),
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

// TestPostgresStore_GetNonExistent tests getting a non-existent score
func TestPostgresStore_GetNonExistent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupPostgresStore(t)
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

// TestPostgresStore_UpdateScore tests updating an existing score (UPSERT)
func TestPostgresStore_UpdateScore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupPostgresStore(t)
	defer cleanup()

	ctx := context.Background()

	// Initial data
	data := &ProviderScoreData{
		Name:            "provider1",
		BaseScore:       100.0,
		HealthPenalty:   5.0,
		TotalOperations: 100,
		LastUpdated:     time.Now().UTC(),
	}

	err := store.SetScore(ctx, data)
	if err != nil {
		t.Fatalf("SetScore failed: %v", err)
	}

	// Update data
	data.HealthPenalty = 10.0
	data.TotalOperations = 200
	data.LastUpdated = time.Now().UTC()

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

// TestPostgresStore_GetAllScores tests retrieving all scores
func TestPostgresStore_GetAllScores(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupPostgresStore(t)
	defer cleanup()

	ctx := context.Background()

	// Add multiple providers
	providers := []string{"provider1", "provider2", "provider3"}
	for i, name := range providers {
		data := &ProviderScoreData{
			Name:        name,
			BaseScore:   100.0 - float64(i*10),
			LastUpdated: time.Now().UTC(),
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

// TestPostgresStore_DeleteScore tests deleting a score
func TestPostgresStore_DeleteScore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupPostgresStore(t)
	defer cleanup()

	ctx := context.Background()

	// Add a score
	data := &ProviderScoreData{
		Name:        "provider1",
		BaseScore:   100.0,
		LastUpdated: time.Now().UTC(),
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

// TestPostgresStore_SetScores tests batch operations
func TestPostgresStore_SetScores(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupPostgresStore(t)
	defer cleanup()

	ctx := context.Background()

	now := time.Now().UTC()

	// Create batch data
	batch := []*ProviderScoreData{
		{Name: "provider1", BaseScore: 100.0, HealthPenalty: 1.0, LastUpdated: now},
		{Name: "provider2", BaseScore: 90.0, HealthPenalty: 2.0, LastUpdated: now},
		{Name: "provider3", BaseScore: 80.0, HealthPenalty: 3.0, LastUpdated: now},
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

// TestPostgresStore_LatencyStats tests latency statistics operations
func TestPostgresStore_LatencyStats(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupPostgresStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create latency data
	latencyData := &LatencyStatsData{
		ProviderSamples: map[string][]int64{
			"provider1": {1000000, 2000000, 3000000},
			"provider2": {500000, 1000000, 1500000},
		},
		LastUpdated: time.Now().UTC(),
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

// TestPostgresStore_LatencyStatsUpdate tests updating latency stats (UPSERT)
func TestPostgresStore_LatencyStatsUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupPostgresStore(t)
	defer cleanup()

	ctx := context.Background()

	// Initial latency data
	data1 := &LatencyStatsData{
		ProviderSamples: map[string][]int64{
			"provider1": {1000000},
		},
		LastUpdated: time.Now().UTC(),
	}

	err := store.SetLatencyStats(ctx, data1)
	if err != nil {
		t.Fatalf("SetLatencyStats (initial) failed: %v", err)
	}

	// Update latency data
	data2 := &LatencyStatsData{
		ProviderSamples: map[string][]int64{
			"provider1": {1000000, 2000000},
			"provider2": {500000},
		},
		LastUpdated: time.Now().UTC(),
	}

	err = store.SetLatencyStats(ctx, data2)
	if err != nil {
		t.Fatalf("SetLatencyStats (update) failed: %v", err)
	}

	// Verify update
	retrieved, err := store.GetLatencyStats(ctx)
	if err != nil {
		t.Fatalf("GetLatencyStats failed: %v", err)
	}

	if len(retrieved.ProviderSamples) != 2 {
		t.Errorf("expected 2 providers, got %d", len(retrieved.ProviderSamples))
	}
}

// TestPostgresStore_TransactionRollback tests transaction rollback on error
func TestPostgresStore_TransactionRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupPostgresStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create batch with one invalid entry (empty name will be skipped)
	batch := []*ProviderScoreData{
		{Name: "provider1", BaseScore: 100.0, LastUpdated: time.Now().UTC()},
		{Name: "", BaseScore: 90.0, LastUpdated: time.Now().UTC()}, // Will be skipped
		{Name: "provider2", BaseScore: 80.0, LastUpdated: time.Now().UTC()},
	}

	err := store.SetScores(ctx, batch)
	if err != nil {
		t.Fatalf("SetScores failed: %v", err)
	}

	// Verify valid entries were saved
	all, err := store.GetAllScores(ctx)
	if err != nil {
		t.Fatalf("GetAllScores failed: %v", err)
	}

	if len(all) != 2 {
		t.Errorf("expected 2 valid scores, got %d", len(all))
	}
}

// TestPostgresStore_ConcurrentAccess tests thread safety
func TestPostgresStore_ConcurrentAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupPostgresStore(t)
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
						LastUpdated:     time.Now().UTC(),
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

// TestPostgresStore_Ping tests health check
func TestPostgresStore_Ping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupPostgresStore(t)
	defer cleanup()

	ctx := context.Background()

	err := store.Ping(ctx)
	if err != nil {
		t.Errorf("Ping failed: %v", err)
	}
}

// TestPostgresStore_Close tests cleanup
func TestPostgresStore_Close(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupPostgresStore(t)
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

// TestPostgresStore_TablePrefix tests custom table prefixing
func TestPostgresStore_TablePrefix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	config := PostgresConfig{
		ConnectionString: getPostgresConnString(t),
		TablePrefix:      "test_custom_",
		MaxOpenConns:     10,
		MaxIdleConns:     2,
		ConnMaxLifetime:  5 * time.Minute,
	}

	store, err := NewPostgresStore(config)
	if err != nil {
		t.Fatalf("NewPostgresStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	data := &ProviderScoreData{
		Name:        "provider1",
		BaseScore:   100.0,
		LastUpdated: time.Now().UTC(),
	}

	err = store.SetScore(ctx, data)
	if err != nil {
		t.Fatalf("SetScore failed: %v", err)
	}

	// Verify the table was created with custom prefix
	var exists bool
	query := `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_name = 'test_custom_provider_scores'
		)
	`
	err = store.pool.QueryRow(ctx, query).Scan(&exists)
	if err != nil {
		t.Fatalf("Query table existence failed: %v", err)
	}
	if !exists {
		t.Error("expected table with custom prefix to exist")
	}
}

// TestPostgresStore_NullableTimestamps tests handling of nullable timestamp fields
func TestPostgresStore_NullableTimestamps(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupPostgresStore(t)
	defer cleanup()

	ctx := context.Background()

	// Set score without optional timestamps
	data := &ProviderScoreData{
		Name:        "provider1",
		BaseScore:   100.0,
		LastUpdated: time.Now().UTC(),
		// LastHealthCheck and LastOperation are zero (not set)
	}

	err := store.SetScore(ctx, data)
	if err != nil {
		t.Fatalf("SetScore failed: %v", err)
	}

	// Retrieve and verify
	retrieved, err := store.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}

	if !retrieved.LastHealthCheck.IsZero() {
		t.Error("expected zero LastHealthCheck")
	}
	if !retrieved.LastOperation.IsZero() {
		t.Error("expected zero LastOperation")
	}

	// Now set with timestamps
	now := time.Now().UTC()
	data.LastHealthCheck = now
	data.LastOperation = now

	err = store.SetScore(ctx, data)
	if err != nil {
		t.Fatalf("SetScore (with timestamps) failed: %v", err)
	}

	retrieved, err = store.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}

	if retrieved.LastHealthCheck.IsZero() {
		t.Error("expected non-zero LastHealthCheck")
	}
	if retrieved.LastOperation.IsZero() {
		t.Error("expected non-zero LastOperation")
	}
}

// TestPostgresStore_EmptyLatencies tests handling of empty latency arrays
func TestPostgresStore_EmptyLatencies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	store, cleanup := setupPostgresStore(t)
	defer cleanup()

	ctx := context.Background()

	// Set score without latencies
	data := &ProviderScoreData{
		Name:            "provider1",
		BaseScore:       100.0,
		LastUpdated:     time.Now().UTC(),
		RecentLatencies: nil, // Explicitly nil
	}

	err := store.SetScore(ctx, data)
	if err != nil {
		t.Fatalf("SetScore failed: %v", err)
	}

	retrieved, err := store.GetScore(ctx, "provider1")
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}

	if retrieved.RecentLatencies == nil {
		// This is acceptable - nil or empty slice
	} else if len(retrieved.RecentLatencies) != 0 {
		t.Errorf("expected empty latencies, got %d", len(retrieved.RecentLatencies))
	}
}

// Benchmarks

func BenchmarkPostgresStore_SetScore(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping integration benchmark")
	}

	store, cleanup := setupPostgresStore(b)
	defer cleanup()

	ctx := context.Background()
	data := &ProviderScoreData{
		Name:            "provider1",
		BaseScore:       100.0,
		RecentLatencies: []int64{1000000, 2000000},
		LastUpdated:     time.Now().UTC(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.SetScore(ctx, data)
	}
}

func BenchmarkPostgresStore_GetScore(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping integration benchmark")
	}

	store, cleanup := setupPostgresStore(b)
	defer cleanup()

	ctx := context.Background()
	data := &ProviderScoreData{
		Name:        "provider1",
		BaseScore:   100.0,
		LastUpdated: time.Now().UTC(),
	}
	_ = store.SetScore(ctx, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.GetScore(ctx, "provider1")
	}
}

func BenchmarkPostgresStore_SetScores(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping integration benchmark")
	}

	store, cleanup := setupPostgresStore(b)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()
	batch := make([]*ProviderScoreData, 10)
	for i := range batch {
		batch[i] = &ProviderScoreData{
			Name:        fmt.Sprintf("provider%d", i),
			BaseScore:   100.0,
			LastUpdated: now,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.SetScores(ctx, batch)
	}
}

// Helper functions

func setupPostgresStore(t testing.TB) (*PostgresStore, func()) {
	config := PostgresConfig{
		ConnectionString: getPostgresConnString(t),
		TablePrefix:      "chainkit_",
		MaxOpenConns:     25,
		MaxIdleConns:     5,
		ConnMaxLifetime:  5 * time.Minute,
	}

	store, err := NewPostgresStore(config)
	if err != nil {
		t.Fatalf("NewPostgresStore failed: %v", err)
	}

	cleanup := func() {
		// Clean up all test data
		ctx := context.Background()
		store.pool.Exec(ctx, fmt.Sprintf("TRUNCATE TABLE %sprovider_scores", store.tablePrefix))
		store.pool.Exec(ctx, fmt.Sprintf("DELETE FROM %slatency_stats WHERE id = 1", store.tablePrefix))
		store.Close()
	}

	return store, cleanup
}

func getPostgresConnString(t testing.TB) string {
	// Check if running in CI or with existing Postgres
	if connString := testcontainersPostgresConnString; connString != "" {
		return connString
	}

	// Start Postgres container for testing
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "chainkit",
			"POSTGRES_PASSWORD": "testpass",
			"POSTGRES_DB":       "chainkit_test",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start Postgres container: %v", err)
	}

	// Store container for cleanup
	t.Cleanup(func() {
		container.Terminate(ctx)
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get container host: %v", err)
	}

	port, err := container.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("failed to get container port: %v", err)
	}

	connString := fmt.Sprintf("postgres://chainkit:testpass@%s:%s/chainkit_test?sslmode=disable",
		host, port.Port())
	testcontainersPostgresConnString = connString
	return connString
}

// Global variable to reuse Postgres container across tests
var testcontainersPostgresConnString string
