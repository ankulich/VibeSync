// Package store defines the Postgres-facing port for VibeSync services.
//
// Each service that persists to Postgres depends on this package for the
// connection pool and the transaction abstraction. The concrete pgx-backed
// implementation (and migrations) ships with the first service that needs it
// (Phase 4, Auth), per the "domain models first" decision. Tests use the
// stub implementations here + testcontainers (libs/testing) for integration.
//
// The TxFunc callback pattern is intentional: it scopes a transaction to a
// lexical block so commit/rollback cannot be forgotten. This is the only
// sanctioned way to run multi-statement mutations across aggregates.
package store

import (
	"context"
	"errors"
)

// ErrNoRows is the canonical "row not found" sentinel. Drivers map their own
// (pgx.ErrNoRows, sql.ErrNoRows) to this so repository code compares one way.
var ErrNoRows = errors.New("store: no rows in result set")

// DB is the queryable surface. A *pgxpool.Pool satisfies this; tests use the
// stub. The methods mirror pgx's naming so the alias is zero-cost.
type DB interface {
	// Exec runs a statement that does not return rows.
	Exec(ctx context.Context, sql string, args ...any) (CommandTag, error)
	// Query runs a statement that returns rows. The caller must close Rows.
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	// QueryRow runs a statement expected to return at most one row.
	QueryRow(ctx context.Context, sql string, args ...any) Row
}

// Tx is the transaction surface. Identical to DB but adds commit/rollback.
type Tx interface {
	DB
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// CommandTag summarizes a write.
type CommandTag struct {
	RowsAffected int64
	Insert       bool
	Update       bool
	Delete       bool
}

// Rows is a forward-only cursor over a result set.
type Rows interface {
	Next() bool
	Close() error
	Err() error
	// Scan copies the current row's columns into dest.
	Scan(dest ...any) error
}

// Row is a single-row result that lazily surfaces errors via Scan.
type Row interface {
	Scan(dest ...any) error
}

// Pool manages the underlying connection pool. Services hold one for the
// process lifetime. BeginTx starts a transaction.
type Pool interface {
	DB
	BeginTx(ctx context.Context) (Tx, error)
	Close()
}

// TxFunc is the body of a transaction. Returning an error rolls back;
// returning nil commits.
type TxFunc func(ctx context.Context, tx Tx) error

// InTx runs fn inside a transaction with the correct commit/rollback
// semantics. The context passed to fn is derived from ctx so cancellation
// aborts both. This is the ONLY sanctioned transaction API in VibeSync.
//
// Rollback is called on error even if the error is context cancellation —
// always-safe semantics, no torn transactions.
func InTx(ctx context.Context, pool Pool, fn TxFunc) (err error) {
	tx, err := pool.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return
		}
		err = tx.Commit(ctx)
	}()
	return fn(ctx, tx)
}
