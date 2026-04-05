package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresPenaltyConfig holds configuration for the Postgres-backed penalty history store.
type PostgresPenaltyConfig struct {
	ConnectionString string

	// TablePrefix is prepended to the table name. Default: "chainkit_"
	TablePrefix string

	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// PostgresPenaltyStore implements PenaltyHistoryStore using PostgreSQL.
//
// Table: {prefix}penalty_history  (append-only, indexed by provider_name + created_at)
//
// PurgeOld issues a DELETE WHERE created_at < NOW() - $1.
type PostgresPenaltyStore struct {
	pool        *pgxpool.Pool
	tablePrefix string
}

// NewPostgresPenaltyStore creates a new Postgres penalty history store.
// It verifies the connection and creates the table if it does not exist.
func NewPostgresPenaltyStore(config PostgresPenaltyConfig) (*PostgresPenaltyStore, error) {
	if config.TablePrefix == "" {
		config.TablePrefix = "chainkit_"
	}
	if config.MaxOpenConns == 0 {
		config.MaxOpenConns = 10
	}
	if config.ConnMaxLifetime == 0 {
		config.ConnMaxLifetime = 5 * time.Minute
	}

	poolCfg, err := pgxpool.ParseConfig(config.ConnectionString)
	if err != nil {
		return nil, fmt.Errorf("postgres penalty store: parse config: %w", err)
	}
	poolCfg.MaxConns = int32(config.MaxOpenConns)
	if config.ConnMaxLifetime > 0 {
		poolCfg.MaxConnLifetime = config.ConnMaxLifetime
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres penalty store: create pool: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres penalty store: ping: %w", err)
	}

	s := &PostgresPenaltyStore{
		pool:        pool,
		tablePrefix: config.TablePrefix,
	}

	if err := s.init(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres penalty store: init schema: %w", err)
	}

	return s, nil
}

func (p *PostgresPenaltyStore) Name() string { return "postgres_penalty" }

func (p *PostgresPenaltyStore) tableName() string {
	return p.tablePrefix + "penalty_history"
}

// init creates the penalty history table and index if they do not exist.
func (p *PostgresPenaltyStore) init(ctx context.Context) error {
	table := p.tableName()
	indexName := "idx_" + table + "_provider_time"

	_, err := p.pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id            BIGSERIAL    PRIMARY KEY,
			provider_name VARCHAR(255) NOT NULL,
			category      VARCHAR(50)  NOT NULL,
			reason        TEXT         NOT NULL,
			amount        DOUBLE PRECISION NOT NULL,
			created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS %s ON %s (provider_name, created_at DESC);
	`, table, indexName, table))
	return err
}

// Append inserts a new penalty record into the history table.
func (p *PostgresPenaltyStore) Append(ctx context.Context, record *PenaltyRecordData) error {
	if record == nil || record.ProviderName == "" {
		return nil
	}

	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	_, err := p.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s (provider_name, category, reason, amount, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, p.tableName()),
		record.ProviderName,
		record.Category,
		record.Reason,
		record.Amount,
		createdAt,
	)
	if err != nil {
		return fmt.Errorf("postgres penalty store: insert: %w", err)
	}
	return nil
}

// GetRecent returns the most recent limit records for a provider (oldest first).
func (p *PostgresPenaltyStore) GetRecent(ctx context.Context, providerName string, limit int) ([]*PenaltyRecordData, error) {
	if limit <= 0 {
		return []*PenaltyRecordData{}, nil
	}

	rows, err := p.pool.Query(ctx, fmt.Sprintf(`
		SELECT provider_name, category, reason, amount, created_at
		FROM %s
		WHERE provider_name = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, p.tableName()), providerName, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres penalty store: query: %w", err)
	}
	defer rows.Close()

	// Collect in DESC order then reverse for chronological (oldest-first) output.
	var buf []*PenaltyRecordData
	for rows.Next() {
		r := &PenaltyRecordData{}
		if err := rows.Scan(&r.ProviderName, &r.Category, &r.Reason, &r.Amount, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres penalty store: scan: %w", err)
		}
		buf = append(buf, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres penalty store: rows: %w", err)
	}

	// Reverse to oldest-first.
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	if buf == nil {
		buf = []*PenaltyRecordData{}
	}
	return buf, nil
}

// PurgeOld deletes all penalty records older than the retention window.
func (p *PostgresPenaltyStore) PurgeOld(ctx context.Context, retentionWindow time.Duration) error {
	cutoff := time.Now().Add(-retentionWindow)
	_, err := p.pool.Exec(ctx, fmt.Sprintf(`
		DELETE FROM %s WHERE created_at < $1
	`, p.tableName()), cutoff)
	if err != nil {
		return fmt.Errorf("postgres penalty store: purge: %w", err)
	}
	return nil
}

func (p *PostgresPenaltyStore) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

func (p *PostgresPenaltyStore) Close() error {
	p.pool.Close()
	return nil
}
