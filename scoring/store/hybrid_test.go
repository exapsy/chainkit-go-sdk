package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupHybridStoreTest creates a hybrid store with real Redis and PostgreSQL containers.
func setupHybridStoreTest(t *testing.T) (*HybridStore, func()) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Start PostgreSQL container
	postgresReq := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "test",
			"POSTGRES_PASSWORD": "test",
			"POSTGRES_DB":       "test",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
	}
	postgresContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: postgresReq,
		Started:          true,
	})
	require.NoError(t, err)

	postgresHost, err := postgresContainer.Host(ctx)
	require.NoError(t, err)
	postgresPort, err := postgresContainer.MappedPort(ctx, "5432")
	require.NoError(t, err)

	// Start Redis container
	redisReq := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}
	redisContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: redisReq,
		Started:          true,
	})
	require.NoError(t, err)

	redisHost, err := redisContainer.Host(ctx)
	require.NoError(t, err)
	redisPort, err := redisContainer.MappedPort(ctx, "6379")
	require.NoError(t, err)

	// Create primary store (PostgreSQL)
	primaryConfig := PostgresConfig{
		ConnectionString: fmt.Sprintf("postgres://test:test@%s:%s/test?sslmode=disable",
			postgresHost, postgresPort.Port()),
		TablePrefix:     "test_",
		MaxOpenConns:    10,
		MaxIdleConns:    2,
		ConnMaxLifetime: 5 * time.Minute,
	}
	primaryStore, err := NewPostgresStore(primaryConfig)
	require.NoError(t, err)

	// Create cache store (Redis)
	cacheConfig := RedisConfig{
		Addr:         fmt.Sprintf("%s:%s", redisHost, redisPort.Port()),
		PoolSize:     10,
		MinIdleConns: 2,
		KeyPrefix:    "test:",
	}
	cacheStore, err := NewRedisStore(cacheConfig)
	require.NoError(t, err)

	// Create hybrid store
	hybridConfig := HybridConfig{
		Primary:      primaryStore,
		Cache:        cacheStore,
		CacheTTL:     5 * time.Minute,
		WriteThrough: true,
		AsyncWrite:   false,
	}
	hybridStore, err := NewHybridStore(hybridConfig)
	require.NoError(t, err)

	// Cleanup function
	cleanup := func() {
		_ = hybridStore.Close()
		_ = postgresContainer.Terminate(ctx)
		_ = redisContainer.Terminate(ctx)
	}

	return hybridStore, cleanup
}

func TestHybridStore_Name(t *testing.T) {
	store, cleanup := setupHybridStoreTest(t)
	defer cleanup()

	name := store.Name()
	assert.Contains(t, name, "hybrid")
	assert.Contains(t, name, "postgres")
	assert.Contains(t, name, "redis")
}

func TestHybridStore_SetAndGetScore(t *testing.T) {
	store, cleanup := setupHybridStoreTest(t)
	defer cleanup()

	ctx := context.Background()

	data := &ProviderScoreData{
		Name:             "provider1",
		BaseScore:        100.0,
		HealthPenalty:    10.0,
		LatencyPenalty:   5.0,
		ErrorPenalty:     2.0,
		RateLimitPenalty: 0.0,
		TotalOperations:  100,
		SuccessfulOps:    95,
		FailedOps:        5,
		LastUpdated:      time.Now(),
		RecentLatencies:  []int64{100000000, 200000000},
	}

	// Set score
	err := store.SetScore(ctx, data)
	require.NoError(t, err)

	// Get score (should come from cache on second read)
	retrieved, err := store.GetScore(ctx, data.Name)
	require.NoError(t, err)
	require.NotNil(t, retrieved)

	assert.Equal(t, data.Name, retrieved.Name)
	assert.Equal(t, data.BaseScore, retrieved.BaseScore)
	assert.Equal(t, data.HealthPenalty, retrieved.HealthPenalty)
	assert.Equal(t, data.TotalOperations, retrieved.TotalOperations)
}

func TestHybridStore_CacheHit(t *testing.T) {
	store, cleanup := setupHybridStoreTest(t)
	defer cleanup()

	ctx := context.Background()

	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	// Write to hybrid store
	err := store.SetScore(ctx, data)
	require.NoError(t, err)

	// Verify data is in both stores
	primaryData, err := store.primary.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Equal(t, data.Name, primaryData.Name)

	cacheData, err := store.cache.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Equal(t, data.Name, cacheData.Name)

	// Read should hit cache
	retrieved, err := store.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Equal(t, data.Name, retrieved.Name)
}

func TestHybridStore_CacheMiss_PopulatesCache(t *testing.T) {
	store, cleanup := setupHybridStoreTest(t)
	defer cleanup()

	ctx := context.Background()

	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	// Write directly to primary (bypass cache)
	err := store.primary.SetScore(ctx, data)
	require.NoError(t, err)

	// Invalidate cache to simulate miss
	store.InvalidateAll()
	_ = store.cache.DeleteScore(ctx, data.Name)

	// Read from hybrid store (should miss cache, hit primary, populate cache)
	retrieved, err := store.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Equal(t, data.Name, retrieved.Name)

	// Verify cache was populated
	time.Sleep(50 * time.Millisecond) // Allow async population if any
	cacheData, err := store.cache.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Equal(t, data.Name, cacheData.Name)
}

func TestHybridStore_CacheTTL(t *testing.T) {
	// Create hybrid store with short TTL
	primaryStore := NewMemoryStore()
	cacheStore := NewMemoryStore()
	hybridStore, err := NewHybridStore(HybridConfig{
		Primary:      primaryStore,
		Cache:        cacheStore,
		CacheTTL:     100 * time.Millisecond,
		WriteThrough: true,
	})
	require.NoError(t, err)
	defer func() { _ = hybridStore.Close() }()

	ctx := context.Background()

	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	// Write to hybrid store
	err = hybridStore.SetScore(ctx, data)
	require.NoError(t, err)

	// Read immediately (cache fresh)
	retrieved1, err := hybridStore.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Equal(t, 100.0, retrieved1.BaseScore)

	// Update primary directly (simulate out-of-band update)
	updatedData := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 200.0,
	}
	err = primaryStore.SetScore(ctx, updatedData)
	require.NoError(t, err)

	// Read before TTL expires (should still get cached value)
	retrieved2, err := hybridStore.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Equal(t, 100.0, retrieved2.BaseScore, "should get cached value")

	// Wait for TTL to expire
	time.Sleep(150 * time.Millisecond)

	// Read after TTL expires (should get updated value from primary)
	retrieved3, err := hybridStore.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Equal(t, 200.0, retrieved3.BaseScore, "should get updated value from primary")
}

func TestHybridStore_WriteThroughMode(t *testing.T) {
	primaryStore := NewMemoryStore()
	cacheStore := NewMemoryStore()
	hybridStore, err := NewHybridStore(HybridConfig{
		Primary:      primaryStore,
		Cache:        cacheStore,
		WriteThrough: true,
		AsyncWrite:   false,
	})
	require.NoError(t, err)
	defer func() { _ = hybridStore.Close() }()

	ctx := context.Background()

	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	// Write through both stores
	err = hybridStore.SetScore(ctx, data)
	require.NoError(t, err)

	// Verify data is in both stores
	primaryData, err := primaryStore.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Equal(t, 100.0, primaryData.BaseScore)

	cacheData, err := cacheStore.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Equal(t, 100.0, cacheData.BaseScore)
}

func TestHybridStore_WriteBehindMode(t *testing.T) {
	primaryStore := NewMemoryStore()
	cacheStore := NewMemoryStore()
	hybridStore, err := NewHybridStore(HybridConfig{
		Primary:      primaryStore,
		Cache:        cacheStore,
		WriteThrough: false, // Write-behind mode
	})
	require.NoError(t, err)
	defer func() { _ = hybridStore.Close() }()

	ctx := context.Background()

	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	// Write to primary only (cache not updated)
	err = hybridStore.SetScore(ctx, data)
	require.NoError(t, err)

	// Verify data is in primary
	primaryData, err := primaryStore.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Equal(t, 100.0, primaryData.BaseScore)

	// Cache should be invalidated (empty or stale)
	// Next read will populate from primary
	retrieved, err := hybridStore.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Equal(t, 100.0, retrieved.BaseScore)
}

func TestHybridStore_InvalidateOnWrite(t *testing.T) {
	primaryStore := NewMemoryStore()
	cacheStore := NewMemoryStore()
	hybridStore, err := NewHybridStore(HybridConfig{
		Primary:           primaryStore,
		Cache:             cacheStore,
		InvalidateOnWrite: true,
	})
	require.NoError(t, err)
	defer func() { _ = hybridStore.Close() }()

	ctx := context.Background()

	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	// Write with invalidation
	err = hybridStore.SetScore(ctx, data)
	require.NoError(t, err)

	// Primary should have data
	primaryData, err := primaryStore.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Equal(t, 100.0, primaryData.BaseScore)

	// Cache should be empty (invalidated)
	cacheData, _ := cacheStore.GetScore(ctx, data.Name)
	assert.Nil(t, cacheData)
}

func TestHybridStore_AsyncWrite(t *testing.T) {
	primaryStore := NewMemoryStore()
	cacheStore := NewMemoryStore()
	hybridStore, err := NewHybridStore(HybridConfig{
		Primary:      primaryStore,
		Cache:        cacheStore,
		WriteThrough: true,
		AsyncWrite:   true, // Async cache writes
	})
	require.NoError(t, err)
	defer func() { _ = hybridStore.Close() }()

	ctx := context.Background()

	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	// Write (cache write happens asynchronously)
	err = hybridStore.SetScore(ctx, data)
	require.NoError(t, err)

	// Primary should have data immediately
	primaryData, err := primaryStore.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Equal(t, 100.0, primaryData.BaseScore)

	// Wait for async write to complete
	time.Sleep(50 * time.Millisecond)

	// Cache should have data now
	cacheData, err := cacheStore.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Equal(t, 100.0, cacheData.BaseScore)
}

func TestHybridStore_SetScores_Batch(t *testing.T) {
	store, cleanup := setupHybridStoreTest(t)
	defer cleanup()

	ctx := context.Background()

	scores := []*ProviderScoreData{
		{Name: "provider1", BaseScore: 100.0},
		{Name: "provider2", BaseScore: 90.0},
		{Name: "provider3", BaseScore: 80.0},
	}

	// Batch write
	err := store.SetScores(ctx, scores)
	require.NoError(t, err)

	// Verify all scores are retrievable
	for _, score := range scores {
		retrieved, err := store.GetScore(ctx, score.Name)
		require.NoError(t, err)
		assert.Equal(t, score.BaseScore, retrieved.BaseScore)
	}
}

func TestHybridStore_GetAllScores(t *testing.T) {
	store, cleanup := setupHybridStoreTest(t)
	defer cleanup()

	ctx := context.Background()

	scores := []*ProviderScoreData{
		{Name: "provider1", BaseScore: 100.0},
		{Name: "provider2", BaseScore: 90.0},
		{Name: "provider3", BaseScore: 80.0},
	}

	// Write all scores
	err := store.SetScores(ctx, scores)
	require.NoError(t, err)

	// Get all scores (reads from primary)
	allScores, err := store.GetAllScores(ctx)
	require.NoError(t, err)
	assert.Len(t, allScores, 3)

	// Verify names
	names := make(map[string]bool)
	for _, s := range allScores {
		names[s.Name] = true
	}
	assert.True(t, names["provider1"])
	assert.True(t, names["provider2"])
	assert.True(t, names["provider3"])
}

func TestHybridStore_DeleteScore(t *testing.T) {
	store, cleanup := setupHybridStoreTest(t)
	defer cleanup()

	ctx := context.Background()

	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	// Set score
	err := store.SetScore(ctx, data)
	require.NoError(t, err)

	// Verify it exists in both stores
	primaryData, err := store.primary.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.NotNil(t, primaryData)

	cacheData, err := store.cache.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.NotNil(t, cacheData)

	// Delete score
	err = store.DeleteScore(ctx, data.Name)
	require.NoError(t, err)

	// Verify it's deleted from both stores
	primaryData, err = store.primary.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Nil(t, primaryData)

	cacheData, err = store.cache.GetScore(ctx, data.Name)
	require.NoError(t, err)
	assert.Nil(t, cacheData)
}

func TestHybridStore_WarmCache(t *testing.T) {
	store, cleanup := setupHybridStoreTest(t)
	defer cleanup()

	ctx := context.Background()

	// Write scores to primary only
	scores := []*ProviderScoreData{
		{Name: "provider1", BaseScore: 100.0},
		{Name: "provider2", BaseScore: 90.0},
		{Name: "provider3", BaseScore: 80.0},
	}
	err := store.primary.SetScores(ctx, scores)
	require.NoError(t, err)

	// Invalidate cache
	store.InvalidateAll()

	// Warm cache
	err = store.WarmCache(ctx)
	require.NoError(t, err)

	// Verify all scores are in cache
	for _, score := range scores {
		cacheData, err := store.cache.GetScore(ctx, score.Name)
		require.NoError(t, err)
		assert.Equal(t, score.BaseScore, cacheData.BaseScore)
	}
}

func TestHybridStore_LatencyStats(t *testing.T) {
	store, cleanup := setupHybridStoreTest(t)
	defer cleanup()

	ctx := context.Background()

	stats := &LatencyStatsData{
		ProviderSamples: map[string][]int64{
			"provider1": {100000000, 200000000},
			"provider2": {150000000, 250000000},
		},
		LastUpdated: time.Now(),
	}

	// Set latency stats (goes to primary only)
	err := store.SetLatencyStats(ctx, stats)
	require.NoError(t, err)

	// Get latency stats (from primary)
	retrieved, err := store.GetLatencyStats(ctx)
	require.NoError(t, err)
	require.NotNil(t, retrieved)

	assert.Len(t, retrieved.ProviderSamples, 2)
	assert.Len(t, retrieved.ProviderSamples["provider1"], 2)
}

func TestHybridStore_Ping(t *testing.T) {
	store, cleanup := setupHybridStoreTest(t)
	defer cleanup()

	ctx := context.Background()

	err := store.Ping(ctx)
	assert.NoError(t, err)
}

func TestHybridStore_Close(t *testing.T) {
	store, cleanup := setupHybridStoreTest(t)
	defer cleanup()

	err := store.Close()
	assert.NoError(t, err)
}

func TestHybridStore_ConcurrentAccess(t *testing.T) {
	store, cleanup := setupHybridStoreTest(t)
	defer cleanup()

	ctx := context.Background()
	numGoroutines := 10
	numOpsPerGoroutine := 20

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			for j := 0; j < numOpsPerGoroutine; j++ {
				providerName := fmt.Sprintf("provider%d", id)
				data := &ProviderScoreData{
					Name:      providerName,
					BaseScore: float64(100 + j),
				}

				// Write
				err := store.SetScore(ctx, data)
				assert.NoError(t, err)

				// Read
				retrieved, err := store.GetScore(ctx, providerName)
				assert.NoError(t, err)
				assert.NotNil(t, retrieved)
			}
		}(i)
	}

	wg.Wait()
}

func TestHybridStore_InvalidateAll(t *testing.T) {
	primaryStore := NewMemoryStore()
	cacheStore := NewMemoryStore()
	hybridStore, err := NewHybridStore(HybridConfig{
		Primary:      primaryStore,
		Cache:        cacheStore,
		WriteThrough: true,
	})
	require.NoError(t, err)
	defer func() { _ = hybridStore.Close() }()

	ctx := context.Background()

	// Write some scores
	scores := []*ProviderScoreData{
		{Name: "provider1", BaseScore: 100.0},
		{Name: "provider2", BaseScore: 90.0},
	}
	err = hybridStore.SetScores(ctx, scores)
	require.NoError(t, err)

	// Invalidate all cache entries
	hybridStore.InvalidateAll()

	// Update primary directly
	updatedData := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 200.0,
	}
	err = primaryStore.SetScore(ctx, updatedData)
	require.NoError(t, err)

	// Read should get updated value from primary
	retrieved, err := hybridStore.GetScore(ctx, "provider1")
	require.NoError(t, err)
	assert.Equal(t, 200.0, retrieved.BaseScore)
}

func TestHybridStore_GetPrimaryAndCache(t *testing.T) {
	primaryStore := NewMemoryStore()
	cacheStore := NewMemoryStore()
	hybridStore, err := NewHybridStore(HybridConfig{
		Primary: primaryStore,
		Cache:   cacheStore,
	})
	require.NoError(t, err)
	defer func() { _ = hybridStore.Close() }()

	// Verify getters return correct stores
	assert.Equal(t, primaryStore, hybridStore.GetPrimary())
	assert.Equal(t, cacheStore, hybridStore.GetCache())
}

func TestHybridStore_ConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      HybridConfig
		expectError bool
	}{
		{
			name: "valid config",
			config: HybridConfig{
				Primary: NewMemoryStore(),
				Cache:   NewMemoryStore(),
			},
			expectError: false,
		},
		{
			name: "missing primary",
			config: HybridConfig{
				Cache: NewMemoryStore(),
			},
			expectError: true,
		},
		{
			name: "missing cache",
			config: HybridConfig{
				Primary: NewMemoryStore(),
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := NewHybridStore(tt.config)
			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, store)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, store)
				_ = store.Close()
			}
		})
	}
}

// Benchmarks

func BenchmarkHybridStore_SetScore(b *testing.B) {
	if testing.Short() {
		b.Skip("Skipping benchmark in short mode")
	}

	primaryStore := NewMemoryStore()
	cacheStore := NewMemoryStore()
	hybridStore, err := NewHybridStore(HybridConfig{
		Primary:      primaryStore,
		Cache:        cacheStore,
		WriteThrough: true,
	})
	require.NoError(b, err)
	defer func() { _ = hybridStore.Close() }()

	ctx := context.Background()
	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hybridStore.SetScore(ctx, data)
	}
}

func BenchmarkHybridStore_GetScore_CacheHit(b *testing.B) {
	if testing.Short() {
		b.Skip("Skipping benchmark in short mode")
	}

	primaryStore := NewMemoryStore()
	cacheStore := NewMemoryStore()
	hybridStore, err := NewHybridStore(HybridConfig{
		Primary:      primaryStore,
		Cache:        cacheStore,
		WriteThrough: true,
	})
	require.NoError(b, err)
	defer func() { _ = hybridStore.Close() }()

	ctx := context.Background()
	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	// Populate cache
	_ = hybridStore.SetScore(ctx, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = hybridStore.GetScore(ctx, "provider1")
	}
}

func BenchmarkHybridStore_GetScore_CacheMiss(b *testing.B) {
	if testing.Short() {
		b.Skip("Skipping benchmark in short mode")
	}

	primaryStore := NewMemoryStore()
	cacheStore := NewMemoryStore()
	hybridStore, err := NewHybridStore(HybridConfig{
		Primary:      primaryStore,
		Cache:        cacheStore,
		CacheTTL:     1 * time.Nanosecond, // Immediate expiration
		WriteThrough: true,
	})
	require.NoError(b, err)
	defer func() { _ = hybridStore.Close() }()

	ctx := context.Background()
	data := &ProviderScoreData{
		Name:      "provider1",
		BaseScore: 100.0,
	}

	// Populate primary
	_ = primaryStore.SetScore(ctx, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hybridStore.InvalidateAll()
		_, _ = hybridStore.GetScore(ctx, "provider1")
	}
}
