package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore implements ScoreStore using PostgreSQL as the backend.
// It provides durable persistence with ACID guarantees and supports
// long-term storage with audit trail capabilities.
type PostgresStore struct {
	pool        *pgxpool.Pool
	config      PostgresConfig
	tablePrefix string
}

// NewPostgresStore creates a new PostgreSQL-based score store.
// It verifies the connection and creates tables if they don't exist.
func NewPostgresStore(config PostgresConfig) (*PostgresStore, error) {
	// Set defaults
	if config.MaxOpenConns == 0 {
		config.MaxOpenConns = 25
	}
	if config.MaxIdleConns == 0 {
		config.MaxIdleConns = 5
	}
	if config.ConnMaxLifetime == 0 {
		config.ConnMaxLifetime = 5 * time.Minute
	}
	if config.TablePrefix == "" {
		config.TablePrefix = "chainkit_"
	}

	// Parse connection string and create pool config
	poolConfig, err := pgxpool.ParseConfig(config.ConnectionString)
	if err != nil {
		return nil, fmt.Errorf("parse connection string: %w", err)
	}

	// Configure connection pool
	poolConfig.MaxConns = int32(config.MaxOpenConns)
	poolConfig.MinConns = int32(config.MaxIdleConns)
	poolConfig.MaxConnLifetime = config.ConnMaxLifetime
	poolConfig.MaxConnIdleTime = 30 * time.Minute

	// Create connection pool
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	// Verify connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	store := &PostgresStore{
		pool:        pool,
		config:      config,
		tablePrefix: config.TablePrefix,
	}

	// Run migrations
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	return store, nil
}

// Name returns the store type identifier.
func (p *PostgresStore) Name() string {
	return "postgres"
}

// migrate creates the necessary database schema.
// This is idempotent and safe to run multiple times.
func (p *PostgresStore) migrate(ctx context.Context) error {
	schema := fmt.Sprintf(`
		-- Provider scores table
		CREATE TABLE IF NOT EXISTS %sprovider_scores (
			provider_name       VARCHAR(255) PRIMARY KEY,
			base_score          DOUBLE PRECISION NOT NULL,
			health_penalty      DOUBLE PRECISION NOT NULL DEFAULT 0,
			latency_penalty     DOUBLE PRECISION NOT NULL DEFAULT 0,
			error_penalty       DOUBLE PRECISION NOT NULL DEFAULT 0,
			rate_limit_penalty  DOUBLE PRECISION NOT NULL DEFAULT 0,
			total_operations    BIGINT NOT NULL DEFAULT 0,
			successful_ops      BIGINT NOT NULL DEFAULT 0,
			failed_ops          BIGINT NOT NULL DEFAULT 0,
			last_health_check   TIMESTAMP WITH TIME ZONE,
			last_operation      TIMESTAMP WITH TIME ZONE,
			recent_latencies    JSONB,
			created_at          TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at          TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);

		-- Latency statistics table (singleton-like table)
		CREATE TABLE IF NOT EXISTS %slatency_stats (
			id                  INTEGER PRIMARY KEY DEFAULT 1,
			provider_samples    JSONB NOT NULL,
			updated_at          TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			CONSTRAINT single_row CHECK (id = 1)
		);

		-- Index for querying by update time
		CREATE INDEX IF NOT EXISTS idx_%sprovider_scores_updated
			ON %sprovider_scores(updated_at DESC);

		-- Index for querying by base score (for sorted queries)
		CREATE INDEX IF NOT EXISTS idx_%sprovider_scores_base
			ON %sprovider_scores(base_score DESC);
	`,
		p.tablePrefix,
		p.tablePrefix,
		p.tablePrefix, p.tablePrefix,
		p.tablePrefix, p.tablePrefix,
	)

	_, err := p.pool.Exec(ctx, schema)
	if err != nil {
		return fmt.Errorf("execute schema: %w", err)
	}

	return nil
}

// scoresTable returns the full table name for provider scores.
func (p *PostgresStore) scoresTable() string {
	return p.tablePrefix + "provider_scores"
}

// latencyTable returns the full table name for latency statistics.
func (p *PostgresStore) latencyTable() string {
	return p.tablePrefix + "latency_stats"
}

// GetScore retrieves the score data for a specific provider.
func (p *PostgresStore) GetScore(ctx context.Context, providerName string) (*ProviderScoreData, error) {
	query := fmt.Sprintf(`
		SELECT
			provider_name, base_score, health_penalty, latency_penalty,
			error_penalty, rate_limit_penalty, total_operations,
			successful_ops, failed_ops, last_health_check, last_operation,
			recent_latencies, updated_at
		FROM %s
		WHERE provider_name = $1
	`, p.scoresTable())

	var data ProviderScoreData
	var latenciesJSON []byte
	var lastHealthCheck, lastOperation *time.Time

	err := p.pool.QueryRow(ctx, query, providerName).Scan(
		&data.Name,
		&data.BaseScore,
		&data.HealthPenalty,
		&data.LatencyPenalty,
		&data.ErrorPenalty,
		&data.RateLimitPenalty,
		&data.TotalOperations,
		&data.SuccessfulOps,
		&data.FailedOps,
		&lastHealthCheck,
		&lastOperation,
		&latenciesJSON,
		&data.LastUpdated,
	)

	if err == pgx.ErrNoRows {
		return nil, nil // Not found, not an error
	}
	if err != nil {
		return nil, fmt.Errorf("query score: %w", err)
	}

	// Handle nullable timestamps
	if lastHealthCheck != nil {
		data.LastHealthCheck = *lastHealthCheck
	}
	if lastOperation != nil {
		data.LastOperation = *lastOperation
	}

	// Deserialize latencies
	if latenciesJSON != nil && len(latenciesJSON) > 0 {
		if err := json.Unmarshal(latenciesJSON, &data.RecentLatencies); err != nil {
			// Log error but don't fail - latencies are optional
			data.RecentLatencies = nil
		}
	}

	return &data, nil
}

// SetScore stores or updates the score data for a provider.
// Uses UPSERT (INSERT ... ON CONFLICT) for atomic updates.
func (p *PostgresStore) SetScore(ctx context.Context, data *ProviderScoreData) error {
	if data == nil || data.Name == "" {
		return nil // Skip invalid data
	}

	latenciesJSON, err := json.Marshal(data.RecentLatencies)
	if err != nil {
		return fmt.Errorf("marshal latencies: %w", err)
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (
			provider_name, base_score, health_penalty, latency_penalty,
			error_penalty, rate_limit_penalty, total_operations,
			successful_ops, failed_ops, last_health_check, last_operation,
			recent_latencies, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (provider_name) DO UPDATE SET
			base_score = EXCLUDED.base_score,
			health_penalty = EXCLUDED.health_penalty,
			latency_penalty = EXCLUDED.latency_penalty,
			error_penalty = EXCLUDED.error_penalty,
			rate_limit_penalty = EXCLUDED.rate_limit_penalty,
			total_operations = EXCLUDED.total_operations,
			successful_ops = EXCLUDED.successful_ops,
			failed_ops = EXCLUDED.failed_ops,
			last_health_check = EXCLUDED.last_health_check,
			last_operation = EXCLUDED.last_operation,
			recent_latencies = EXCLUDED.recent_latencies,
			updated_at = EXCLUDED.updated_at
	`, p.scoresTable())

	// Handle nullable timestamps
	var lastHealthCheck, lastOperation interface{}
	if !data.LastHealthCheck.IsZero() {
		lastHealthCheck = data.LastHealthCheck
	}
	if !data.LastOperation.IsZero() {
		lastOperation = data.LastOperation
	}

	// Use current time if LastUpdated is zero
	updatedAt := data.LastUpdated
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}

	_, err = p.pool.Exec(ctx, query,
		data.Name,
		data.BaseScore,
		data.HealthPenalty,
		data.LatencyPenalty,
		data.ErrorPenalty,
		data.RateLimitPenalty,
		data.TotalOperations,
		data.SuccessfulOps,
		data.FailedOps,
		lastHealthCheck,
		lastOperation,
		latenciesJSON,
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert score: %w", err)
	}

	return nil
}

// GetAllScores retrieves score data for all providers.
func (p *PostgresStore) GetAllScores(ctx context.Context) ([]*ProviderScoreData, error) {
	query := fmt.Sprintf(`
		SELECT
			provider_name, base_score, health_penalty, latency_penalty,
			error_penalty, rate_limit_penalty, total_operations,
			successful_ops, failed_ops, last_health_check, last_operation,
			recent_latencies, updated_at
		FROM %s
		ORDER BY base_score DESC
	`, p.scoresTable())

	rows, err := p.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query all scores: %w", err)
	}
	defer rows.Close()

	var scores []*ProviderScoreData
	for rows.Next() {
		var data ProviderScoreData
		var latenciesJSON []byte
		var lastHealthCheck, lastOperation *time.Time

		err := rows.Scan(
			&data.Name,
			&data.BaseScore,
			&data.HealthPenalty,
			&data.LatencyPenalty,
			&data.ErrorPenalty,
			&data.RateLimitPenalty,
			&data.TotalOperations,
			&data.SuccessfulOps,
			&data.FailedOps,
			&lastHealthCheck,
			&lastOperation,
			&latenciesJSON,
			&data.LastUpdated,
		)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		// Handle nullable timestamps
		if lastHealthCheck != nil {
			data.LastHealthCheck = *lastHealthCheck
		}
		if lastOperation != nil {
			data.LastOperation = *lastOperation
		}

		// Deserialize latencies
		if latenciesJSON != nil && len(latenciesJSON) > 0 {
			json.Unmarshal(latenciesJSON, &data.RecentLatencies)
		}

		scores = append(scores, &data)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}

	return scores, nil
}

// DeleteScore removes the score data for a specific provider.
func (p *PostgresStore) DeleteScore(ctx context.Context, providerName string) error {
	query := fmt.Sprintf(`DELETE FROM %s WHERE provider_name = $1`, p.scoresTable())

	_, err := p.pool.Exec(ctx, query, providerName)
	if err != nil {
		return fmt.Errorf("delete score: %w", err)
	}

	return nil
}

// SetScores stores or updates multiple provider scores in a single transaction.
// This is more efficient than calling SetScore multiple times.
func (p *PostgresStore) SetScores(ctx context.Context, data []*ProviderScoreData) error {
	if len(data) == 0 {
		return nil
	}

	// Start transaction
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Prepare statement for batch insert
	query := fmt.Sprintf(`
		INSERT INTO %s (
			provider_name, base_score, health_penalty, latency_penalty,
			error_penalty, rate_limit_penalty, total_operations,
			successful_ops, failed_ops, last_health_check, last_operation,
			recent_latencies, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (provider_name) DO UPDATE SET
			base_score = EXCLUDED.base_score,
			health_penalty = EXCLUDED.health_penalty,
			latency_penalty = EXCLUDED.latency_penalty,
			error_penalty = EXCLUDED.error_penalty,
			rate_limit_penalty = EXCLUDED.rate_limit_penalty,
			total_operations = EXCLUDED.total_operations,
			successful_ops = EXCLUDED.successful_ops,
			failed_ops = EXCLUDED.failed_ops,
			last_health_check = EXCLUDED.last_health_check,
			last_operation = EXCLUDED.last_operation,
			recent_latencies = EXCLUDED.recent_latencies,
			updated_at = EXCLUDED.updated_at
	`, p.scoresTable())

	// Execute batch upserts
	for _, score := range data {
		if score == nil || score.Name == "" {
			continue
		}

		latenciesJSON, err := json.Marshal(score.RecentLatencies)
		if err != nil {
			return fmt.Errorf("marshal latencies for %s: %w", score.Name, err)
		}

		// Handle nullable timestamps
		var lastHealthCheck, lastOperation interface{}
		if !score.LastHealthCheck.IsZero() {
			lastHealthCheck = score.LastHealthCheck
		}
		if !score.LastOperation.IsZero() {
			lastOperation = score.LastOperation
		}

		// Use current time if LastUpdated is zero
		updatedAt := score.LastUpdated
		if updatedAt.IsZero() {
			updatedAt = time.Now()
		}

		_, err = tx.Exec(ctx, query,
			score.Name,
			score.BaseScore,
			score.HealthPenalty,
			score.LatencyPenalty,
			score.ErrorPenalty,
			score.RateLimitPenalty,
			score.TotalOperations,
			score.SuccessfulOps,
			score.FailedOps,
			lastHealthCheck,
			lastOperation,
			latenciesJSON,
			updatedAt,
		)
		if err != nil {
			return fmt.Errorf("upsert score %s: %w", score.Name, err)
		}
	}

	// Commit transaction
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// GetLatencyStats retrieves the global latency statistics.
func (p *PostgresStore) GetLatencyStats(ctx context.Context) (*LatencyStatsData, error) {
	query := fmt.Sprintf(`
		SELECT provider_samples, updated_at
		FROM %s
		WHERE id = 1
	`, p.latencyTable())

	var samplesJSON []byte
	var stats LatencyStatsData

	err := p.pool.QueryRow(ctx, query).Scan(&samplesJSON, &stats.LastUpdated)
	if err == pgx.ErrNoRows {
		return nil, nil // Not found, not an error
	}
	if err != nil {
		return nil, fmt.Errorf("query latency stats: %w", err)
	}

	// Deserialize samples
	if len(samplesJSON) > 0 {
		if err := json.Unmarshal(samplesJSON, &stats.ProviderSamples); err != nil {
			return nil, fmt.Errorf("unmarshal provider samples: %w", err)
		}
	}

	return &stats, nil
}

// SetLatencyStats stores the global latency statistics.
func (p *PostgresStore) SetLatencyStats(ctx context.Context, data *LatencyStatsData) error {
	if data == nil {
		return nil
	}

	samplesJSON, err := json.Marshal(data.ProviderSamples)
	if err != nil {
		return fmt.Errorf("marshal provider samples: %w", err)
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (id, provider_samples, updated_at)
		VALUES (1, $1, $2)
		ON CONFLICT (id) DO UPDATE SET
			provider_samples = EXCLUDED.provider_samples,
			updated_at = EXCLUDED.updated_at
	`, p.latencyTable())

	updatedAt := data.LastUpdated
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}

	_, err = p.pool.Exec(ctx, query, samplesJSON, updatedAt)
	if err != nil {
		return fmt.Errorf("upsert latency stats: %w", err)
	}

	return nil
}

// Close releases the database connection pool.
func (p *PostgresStore) Close() error {
	p.pool.Close()
	return nil
}

// Ping checks if the database is accessible.
func (p *PostgresStore) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// Ensure PostgresStore implements ScoreStore
var _ ScoreStore = (*PostgresStore)(nil)
