// Package migrate wraps golang-migrate to run Auth's SQL migrations at
// startup. Migrations are embedded at the module root (see
// migrations_embed.go) so the binary is self-contained; the relay and server
// don't need the migrations/ directory at runtime.
//
// golang-migrate is the repo-wide standard (ADR-0013). Each service that owns
// a schema reproduces this pattern: an embed.FS of its migrations and a Run
// function that takes a database URL.
package migrate

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // pgx driver registration
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"vibesync/apps/auth-service/migrations"
)

// sourceFS wraps the embedded migrations for golang-migrate's iofs source.
// The embed directive lives in migrations/embed.go.
var sourceFS = migrations.FS

// Run applies all up migrations against databaseURL. databaseURL must be a
// pgx/v5 connection string (e.g. "postgres://user:pass@host:5432/auth").
//
// Idempotent: if the DB is already at the latest version, Run is a no-op.
// Returns a typed error if migrations fail mid-way (the DB may be left in a
// partially-migrated state; operational recovery is via the `migrate` CLI).
func Run(_ context.Context, databaseURL string) error {
	d, err := iofs.New(sourceFS, ".")
	if err != nil {
		return fmt.Errorf("migrate: embed source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", d, databaseURL)
	if err != nil {
		return fmt.Errorf("migrate: init: %w", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}

// Version returns the current migration version (number + dirty flag), useful
// for startup logs. Returns (0, false, nil) on a fresh database.
func Version(databaseURL string) (uint, bool, error) {
	d, err := iofs.New(sourceFS, ".")
	if err != nil {
		return 0, false, fmt.Errorf("migrate: embed source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", d, databaseURL)
	if err != nil {
		return 0, false, fmt.Errorf("migrate: init: %w", err)
	}
	defer m.Close()
	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return v, dirty, nil
}
