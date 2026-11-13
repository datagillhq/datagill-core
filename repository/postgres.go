// Package repository defines the persistence interface and its PostgreSQL
// implementation. All methods are idempotent — callers may safely retry any
// operation without risk of duplicate data.
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("not found")

// PostgresRepository implements Repository using PostgreSQL via pgx.
type PostgresRepository struct {
	pool          *pgxpool.Pool
	encryptionKey []byte // 32-byte AES-256 key for API key encryption
}

// NewPostgresRepository creates a new PostgresRepository with a connection pool.
// encryptionKey must be exactly 32 bytes (AES-256).
//
// Pool settings can be configured via connection string parameters:
//   - pool_max_conns (default: 4)
//   - pool_min_conns (default: 0)
//   - pool_max_conn_lifetime (e.g., "1h")
//   - pool_max_conn_idle_time (e.g., "30m")
//   - pool_health_check_period (e.g., "1m")
//
// Example: postgres://user:pass@host/db?pool_max_conns=20&pool_min_conns=2
func NewPostgresRepository(ctx context.Context, connString string, encryptionKey []byte) (*PostgresRepository, error) {
	if len(encryptionKey) != 32 {
		return nil, fmt.Errorf("encryptionKey must be 32 bytes, got %d", len(encryptionKey))
	}
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return &PostgresRepository{pool: pool, encryptionKey: encryptionKey}, nil
}

// Close closes the connection pool.
func (r *PostgresRepository) Close() {
	r.pool.Close()
}

// orNow returns t, or the current UTC time when t is zero.
//
// Save methods pass created_at and updated_at as explicit parameters, which bypasses the
// columns' DEFAULT NOW(). A caller that leaves them unset would otherwise store
// 0001-01-01 instead of when the row was written.
func orNow(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t
}

// Compile-time check that PostgresRepository implements Repository.
var _ Repository = (*PostgresRepository)(nil)
