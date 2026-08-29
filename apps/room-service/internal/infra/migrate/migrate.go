// Package migrate wraps golang-migrate for the Room Service. Mirrors the
// auth-service pattern (ADR-0013).
package migrate

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	// Registers the pgx/v5 database driver with golang-migrate so its DSN can be
	// used as a target; imported for its init side effect only.
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"vibesync/apps/room-service/migrations"
)

var sourceFS = migrations.FS

// Run applies all up migrations. Idempotent.
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
