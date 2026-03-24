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

// Compile-time check that PostgresRepository implements Repository.
var _ Repository = (*PostgresRepository)(nil)
