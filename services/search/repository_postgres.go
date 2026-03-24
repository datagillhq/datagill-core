package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresSearchRepository implements SearchRepository against the app Postgres database.
type PostgresSearchRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresSearchRepository creates a connection pool and verifies connectivity.
func NewPostgresSearchRepository(ctx context.Context, connString string) (*PostgresSearchRepository, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("NewPostgresSearchRepository: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("NewPostgresSearchRepository ping: %w", err)
	}
	return &PostgresSearchRepository{pool: pool}, nil
}

func (r *PostgresSearchRepository) Close() {
	r.pool.Close()
}

// ListFlags returns flags for the given org, with optional source and environment filters.
// Flags are ordered by most recently updated first.
func (r *PostgresSearchRepository) ListFlags(ctx context.Context, orgID string, filters FlagFilters) ([]FlagSummary, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			key::text,
			COALESCE(data->>'name', ''),
			COALESCE(data->'source'->>'provider', ''),
			updated_at
		FROM feature_flags
		WHERE org_id = $1::uuid
		  AND ($2 = '' OR data->'source'->>'provider' = $2)
		  AND ($3 = '' OR data->'environments' ? $3)
		ORDER BY updated_at DESC
	`, orgID, filters.Source, filters.Environment)
	if err != nil {
		return nil, fmt.Errorf("ListFlags: %w", err)
	}
	defer rows.Close()

	var flags []FlagSummary
	for rows.Next() {
		var f FlagSummary
		if err := rows.Scan(&f.FlagKey, &f.Name, &f.Source, &f.LastEventAt); err != nil {
			return nil, fmt.Errorf("ListFlags scan: %w", err)
		}
		flags = append(flags, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListFlags rows: %w", err)
	}
	return flags, nil
}
