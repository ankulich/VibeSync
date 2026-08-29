// Package postgres contains the Postgres-backed adapters for the User Service.
// Mirrors the auth-service pattern (ADR-0001): pool wraps pgxpool; repos take
// pgx.Tx so the use case controls atomicity.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"vibesync/apps/user-service/internal/ports"
)

// Pool wraps *pgxpool.Pool and implements ports.Pool.
type Pool struct {
	pool *pgxpool.Pool
}

// NewPool constructs a Pool from a pgx connection string.
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

// BeginTx starts a transaction.
func (p *Pool) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return p.pool.BeginTx(ctx, pgx.TxOptions{})
}

// Pool exposes the underlying pool for direct reads.
func (p *Pool) Pool() *pgxpool.Pool { return p.pool }

// Close releases the pool's connections.
func (p *Pool) Close() { p.pool.Close() }

// Ping verifies connectivity.
func (p *Pool) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

var _ ports.Pool = (*Pool)(nil)
