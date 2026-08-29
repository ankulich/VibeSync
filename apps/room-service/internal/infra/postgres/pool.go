// Package postgres contains the Postgres-backed adapters for the Room Service.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"vibesync/apps/room-service/internal/ports"
)

// Pool wraps *pgxpool.Pool and implements ports.Pool.
type Pool struct {
	pool *pgxpool.Pool
}

// NewPool parses the database URL and returns a connected Pool.
func NewPool(ctx context.Context, databaseURL string, maxConns int32) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	return &Pool{pool: p}, nil
}

// BeginTx starts a new transaction from the pool.
func (p *Pool) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return p.pool.BeginTx(ctx, pgx.TxOptions{})
}

// Pool returns the underlying *pgxpool.Pool.
func (p *Pool) Pool() *pgxpool.Pool { return p.pool }

// Close releases all pool connections.
func (p *Pool) Close() { p.pool.Close() }

// Ping verifies the pool can reach the database.
func (p *Pool) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

var _ ports.Pool = (*Pool)(nil)
