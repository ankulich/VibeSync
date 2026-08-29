// Package postgres contains the Postgres-backed adapters for Auth's
// repository ports. Each repository is a thin wrapper over pgx that maps
// domain entities to SQL rows.
//
// Conventions across all repos:
//   - Methods take a pgx.Tx (NOT the pool) so the use case controls atomicity.
//     Read-only methods also take a tx for uniformity; the use case passes
//     either a real tx or a read-only wrapper from Pool().
//   - Not-found is reported as ports.ErrNotFound (errors.Is-comparable).
//   - Scan helpers live in scan.go to keep the per-entity files focused.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"vibesync/apps/auth-service/internal/ports"
)

// Pool wraps *pgxpool.Pool and implements ports.Pool. Constructed once at
// startup; the use case uses it for both transactions (BeginTx) and direct
// reads (Pool().QueryRow) when a tx isn't needed.
type Pool struct {
	pool *pgxpool.Pool
}

// NewPool constructs a Pool from a pgx connection string. The pool is sized
// per cfg.MaxConns (default 10).
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

// BeginTx starts a transaction. The caller (use case) commits/rolls back.
func (p *Pool) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return p.pool.BeginTx(ctx, pgx.TxOptions{})
}

// Pool exposes the underlying *pgxpool.Pool for direct reads or for
// constructing a read-only pgx.Tx substitute via pool.Acquire.
func (p *Pool) Pool() *pgxpool.Pool { return p.pool }

// Close releases the pool's connections. Idempotent.
func (p *Pool) Close() { p.pool.Close() }

// Ping verifies the pool can reach the database. Used by main.go for
// startup readiness.
func (p *Pool) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// Compile-time interface checks. If a method signature drifts from the port,
// the build fails here rather than at the call site.
var (
	_ ports.Pool = (*Pool)(nil)
)
