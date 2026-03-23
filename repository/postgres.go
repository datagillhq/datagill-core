package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("not found")

// PostgresRepository implements Repository using PostgreSQL via pgx.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresRepository creates a new PostgresRepository with a connection pool.
func NewPostgresRepository(ctx context.Context, connString string) (*PostgresRepository, error) {
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return &PostgresRepository{pool: pool}, nil
}

// Close closes the connection pool.
func (r *PostgresRepository) Close() {
	r.pool.Close()
}

// SaveSourceRecord persists a raw provider API response.
// Uses ON CONFLICT DO NOTHING since source_records is append-only.
func (r *PostgresRepository) SaveSourceRecord(ctx context.Context, rec models.SourceRecord) error {
	query := `
		INSERT INTO source_records (key, integration_id, org_id, source_type, data, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (key) DO NOTHING
	`

	_, err := r.pool.Exec(ctx, query,
		rec.Key,
		rec.IntegrationID,
		rec.OrgID,
		rec.SourceType,
		rec.Data,
		rec.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("SaveSourceRecord: %w", err)
	}

	return nil
}

// SaveFeatureFlag upserts the normalized state of a feature flag.
func (r *PostgresRepository) SaveFeatureFlag(ctx context.Context, f models.FeatureFlag) error {
	dataJSON, err := json.Marshal(f.Data)
	if err != nil {
		return fmt.Errorf("SaveFeatureFlag marshal data: %w", err)
	}

	query := `
		INSERT INTO feature_flags (key, source_record_key, integration_id, org_id, state, data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (key) DO UPDATE SET
			source_record_key = EXCLUDED.source_record_key,
			state = EXCLUDED.state,
			data = EXCLUDED.data,
			updated_at = EXCLUDED.updated_at
	`

	_, err = r.pool.Exec(ctx, query,
		f.Key,
		f.SourceRecordKey,
		f.IntegrationID,
		f.OrgID,
		f.State,
		dataJSON,
		f.CreatedAt,
		f.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("SaveFeatureFlag: %w", err)
	}

	return nil
}

// SaveChangelog upserts a normalized changelog entry.
func (r *PostgresRepository) SaveChangelog(ctx context.Context, c models.FeatureFlagChangelog) error {
	dataJSON, err := json.Marshal(c.Data)
	if err != nil {
		return fmt.Errorf("SaveChangelog marshal data: %w", err)
	}

	query := `
		INSERT INTO feature_flag_changelog (key, source_record_key, feature_flag_key, integration_id, org_id, event_type, state, occurred_at, data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (key) DO UPDATE SET
			state = EXCLUDED.state,
			data = EXCLUDED.data,
			updated_at = EXCLUDED.updated_at
	`

	_, err = r.pool.Exec(ctx, query,
		c.Key,
		c.SourceRecordKey,
		c.FeatureFlagKey,
		c.IntegrationID,
		c.OrgID,
		c.EventType,
		c.State,
		c.OccurredAt,
		dataJSON,
		c.CreatedAt,
		c.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("SaveChangelog: %w", err)
	}

	return nil
}

// GetIntegration returns a single integration by its key.
func (r *PostgresRepository) GetIntegration(ctx context.Context, integrationKey uuid.UUID) (models.Integration, error) {
	query := `
		SELECT id, key, org_id, source, api_key, config, state, created_at, updated_at
		FROM integrations
		WHERE key = $1
	`

	var i models.Integration
	var configJSON []byte

	err := r.pool.QueryRow(ctx, query, integrationKey).Scan(
		&i.ID,
		&i.Key,
		&i.OrgID,
		&i.Source,
		&i.APIKey,
		&configJSON,
		&i.State,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Integration{}, ErrNotFound
		}
		return models.Integration{}, fmt.Errorf("GetIntegration: %w", err)
	}

	if err := json.Unmarshal(configJSON, &i.Config); err != nil {
		return models.Integration{}, fmt.Errorf("GetIntegration unmarshal config: %w", err)
	}

	return i, nil
}

// ListIntegrations returns all active integrations for an org.
func (r *PostgresRepository) ListIntegrations(ctx context.Context, orgID uuid.UUID) ([]models.Integration, error) {
	query := `
		SELECT id, key, org_id, source, api_key, config, state, created_at, updated_at
		FROM integrations
		WHERE org_id = $1 AND state = 'active'
		ORDER BY created_at
	`

	rows, err := r.pool.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("ListIntegrations query: %w", err)
	}
	defer rows.Close()

	var integrations []models.Integration
	for rows.Next() {
		var i models.Integration
		var configJSON []byte

		if err := rows.Scan(
			&i.ID,
			&i.Key,
			&i.OrgID,
			&i.Source,
			&i.APIKey,
			&configJSON,
			&i.State,
			&i.CreatedAt,
			&i.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("ListIntegrations scan: %w", err)
		}

		if err := json.Unmarshal(configJSON, &i.Config); err != nil {
			return nil, fmt.Errorf("ListIntegrations unmarshal config: %w", err)
		}

		integrations = append(integrations, i)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListIntegrations rows: %w", err)
	}

	return integrations, nil
}

// Compile-time check that PostgresRepository implements Repository.
var _ Repository = (*PostgresRepository)(nil)
