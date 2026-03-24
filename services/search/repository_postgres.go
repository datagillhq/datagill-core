package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
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

// GetFlag returns the full detail for a single flag scoped to the given org.
// Returns ErrNotFound if no matching flag exists.
func (r *PostgresSearchRepository) GetFlag(ctx context.Context, orgID, flagKey string) (FlagDetail, error) {
	var f FlagDetail
	err := r.pool.QueryRow(ctx, `
		SELECT
			key::text,
			COALESCE(data->>'name', ''),
			COALESCE(data->'source'->>'provider', ''),
			COALESCE(data->>'description', ''),
			created_at,
			updated_at
		FROM feature_flags
		WHERE org_id = $1::uuid
		  AND key = $2::uuid
	`, orgID, flagKey).Scan(&f.FlagKey, &f.Name, &f.Source, &f.Description, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		if isNotFound(err) {
			return FlagDetail{}, ErrNotFound
		}
		return FlagDetail{}, fmt.Errorf("GetFlag: %w", err)
	}
	return f, nil
}

// GetFlagHistory returns changelog entries for a specific flag, ordered by
// most recent first, with page-based pagination.
func (r *PostgresSearchRepository) GetFlagHistory(ctx context.Context, orgID, flagKey string, p Pagination) ([]ChangelogEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			key::text,
			feature_flag_key::text,
			org_id::text,
			COALESCE(data->>'project', ''),
			COALESCE(data->>'environment', ''),
			event_type,
			COALESCE(data->'actor'->>'email', ''),
			occurred_at,
			COALESCE((data->'actor'->>'is_automated')::boolean, false),
			COALESCE((data->>'is_rollback')::boolean, false)
		FROM feature_flag_changelog
		WHERE org_id = $1::uuid
		  AND feature_flag_key = $2::uuid
		ORDER BY occurred_at DESC
		LIMIT $3 OFFSET $4
	`, orgID, flagKey, p.Limit, (p.Page-1)*p.Limit)
	if err != nil {
		return nil, fmt.Errorf("GetFlagHistory: %w", err)
	}
	defer rows.Close()

	var entries []ChangelogEntry
	for rows.Next() {
		var e ChangelogEntry
		if err := rows.Scan(&e.ID, &e.FlagKey, &e.OrgID, &e.Source, &e.Environment,
			&e.EventType, &e.ActorEmail, &e.OccurredAt, &e.IsAutomated, &e.IsRollback); err != nil {
			return nil, fmt.Errorf("GetFlagHistory scan: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GetFlagHistory rows: %w", err)
	}
	return entries, nil
}

// isNotFound returns true when pgx finds no rows.
func isNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
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
