// Package repository defines the persistence interface and its PostgreSQL
// implementation. All methods are idempotent — callers may safely retry any
// operation without risk of duplicate data.
package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("not found")

// PostgresRepository implements Repository using PostgreSQL via pgx.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresRepository creates a new PostgresRepository with a connection pool.
// Pool settings can be configured via connection string parameters:
//   - pool_max_conns (default: 4)
//   - pool_min_conns (default: 0)
//   - pool_max_conn_lifetime (e.g., "1h")
//   - pool_max_conn_idle_time (e.g., "30m")
//   - pool_health_check_period (e.g., "1m")
//
// Example: postgres://user:pass@host/db?pool_max_conns=20&pool_min_conns=2
func NewPostgresRepository(ctx context.Context, connString string) (*PostgresRepository, error) {
	pool, err := pgxpool.New(ctx, connString)
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

// Compile-time check that PostgresRepository implements Repository.
var _ Repository = (*PostgresRepository)(nil)
