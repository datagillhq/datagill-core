package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Compile-time check that PostgresSearchRepository satisfies SearchRepository.
var _ SearchRepository = (*PostgresSearchRepository)(nil)

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

// observe records datagill_db_query_duration_seconds for a completed query.
// Pass a pointer to the method's named error return so the status reflects
// the final resolved error, not the value at defer call time.
func (r *PostgresSearchRepository) observe(query string, start time.Time, errp *error) {
	status := "ok"
	if *errp != nil {
		switch {
		case errors.Is(*errp, ErrNotFound):
			status = "not_found"
		case errors.Is(*errp, context.DeadlineExceeded):
			status = "timeout"
		default:
			status = "error"
		}
	}
	dbQueryDuration.WithLabelValues(query, status).Observe(time.Since(start).Seconds())
}

// GetFlag returns the full detail for a single flag scoped to the given org.
// Returns ErrNotFound if no matching flag exists.
func (r *PostgresSearchRepository) GetFlag(ctx context.Context, orgID, flagKey string) (_ FlagDetail, err error) {
	defer r.observe("get_flag", time.Now(), &err)
	var f FlagDetail
	err = r.pool.QueryRow(ctx, `
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
func (r *PostgresSearchRepository) GetFlagHistory(ctx context.Context, orgID, flagKey string, p Pagination) (_ []ChangelogEntry, err error) {
	defer r.observe("get_flag_history", time.Now(), &err)
	rows, err := r.pool.Query(ctx, `
		SELECT
			c.key::text,
			c.feature_flag_key::text,
			c.org_id::text,
			COALESCE(f.data->'source'->>'provider', ''),
			COALESCE(c.data->'environments', '[]'::jsonb),
			c.event_type,
			COALESCE(c.data->'actor'->>'email', ''),
			c.occurred_at,
			COALESCE(c.data->>'change_type', '')
		FROM feature_flag_changelog c
		JOIN feature_flags f ON f.key = c.feature_flag_key
		WHERE c.org_id = $1::uuid
		  AND c.feature_flag_key = $2::uuid
		ORDER BY c.occurred_at DESC
		LIMIT $3 OFFSET $4
	`, orgID, flagKey, p.Limit, (p.Page-1)*p.Limit)
	if err != nil {
		return nil, fmt.Errorf("GetFlagHistory: %w", err)
	}
	defer rows.Close()

	var entries []ChangelogEntry
	for rows.Next() {
		var e ChangelogEntry
		var envsBytes []byte
		if err := rows.Scan(&e.ID, &e.FlagKey, &e.OrgID, &e.Source, &envsBytes,
			&e.EventType, &e.ActorEmail, &e.OccurredAt, &e.ChangeType); err != nil {
			return nil, fmt.Errorf("GetFlagHistory scan: %w", err)
		}
		if len(envsBytes) > 0 {
			_ = json.Unmarshal(envsBytes, &e.Environments) // tolerate NULL/malformed; nil slice is a safe empty value
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

// GetTimeline returns changelog entries across all flags for the given org,
// ordered by most recent first, with page-based pagination.
func (r *PostgresSearchRepository) GetTimeline(ctx context.Context, orgID string, p Pagination) (_ []ChangelogEntry, err error) {
	defer r.observe("get_timeline", time.Now(), &err)
	rows, err := r.pool.Query(ctx, `
		SELECT
			c.key::text,
			c.feature_flag_key::text,
			c.org_id::text,
			COALESCE(f.data->'source'->>'provider', ''),
			COALESCE(c.data->'environments', '[]'::jsonb),
			c.event_type,
			COALESCE(c.data->'actor'->>'email', ''),
			c.occurred_at,
			COALESCE(c.data->>'change_type', '')
		FROM feature_flag_changelog c
		JOIN feature_flags f ON f.key = c.feature_flag_key
		WHERE c.org_id = $1::uuid
		ORDER BY c.occurred_at DESC
		LIMIT $2 OFFSET $3
	`, orgID, p.Limit, (p.Page-1)*p.Limit)
	if err != nil {
		return nil, fmt.Errorf("GetTimeline: %w", err)
	}
	defer rows.Close()

	var entries []ChangelogEntry
	for rows.Next() {
		var e ChangelogEntry
		var envsBytes []byte
		if err := rows.Scan(&e.ID, &e.FlagKey, &e.OrgID, &e.Source, &envsBytes,
			&e.EventType, &e.ActorEmail, &e.OccurredAt, &e.ChangeType); err != nil {
			return nil, fmt.Errorf("GetTimeline scan: %w", err)
		}
		if len(envsBytes) > 0 {
			_ = json.Unmarshal(envsBytes, &e.Environments)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GetTimeline rows: %w", err)
	}
	return entries, nil
}

// escapeLike escapes LIKE special characters so user input is treated as a
// literal string. Postgres LIKE treats % (any sequence) and _ (any single
// character) as wildcards; \ is the default escape character.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// Search returns flags whose name matches the query string, scored by match
// quality (prefix match ranks above substring match). Returns up to req.Limit
// results scoped to req.OrgID.
func (r *PostgresSearchRepository) Search(ctx context.Context, req SearchRequest) (_ []SearchResult, err error) {
	if req.Query == "" {
		return nil, nil
	}
	defer r.observe("search", time.Now(), &err)
	safe := escapeLike(req.Query)
	pattern := "%" + safe + "%"
	rows, err := r.pool.Query(ctx, `
		SELECT
			key::text,
			key::text,
			org_id::text,
			COALESCE(data->'source'->>'provider', ''),
			COALESCE(data->>'description', ''),
			COALESCE(data->>'name', ''),
			updated_at,
			CASE
				WHEN lower(data->>'name') LIKE lower($2) ESCAPE '\' THEN 1.0
				ELSE 0.5
			END AS score
		FROM feature_flags
		WHERE org_id = $1::uuid
		  AND lower(data->>'name') LIKE lower($3) ESCAPE '\'
		ORDER BY score DESC, updated_at DESC
		LIMIT $4
	`, req.OrgID, safe+"%", pattern, req.Limit)
	if err != nil {
		return nil, fmt.Errorf("Search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.ID, &r.FlagKey, &r.OrgID, &r.Source, &r.Description,
			&r.Title, &r.LastEventAt, &r.Score); err != nil {
			return nil, fmt.Errorf("Search scan: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Search rows: %w", err)
	}
	return results, nil
}

// ListFlags returns flags for the given org, with optional source and environment filters.
// Flags are ordered by most recently updated first.
func (r *PostgresSearchRepository) ListFlags(ctx context.Context, orgID string, filters FlagFilters) (_ []FlagSummary, err error) {
	defer r.observe("list_flags", time.Now(), &err)
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
