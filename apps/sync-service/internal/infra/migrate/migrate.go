// Package migrate wraps golang-migrate for the Sync Service. Mirrors the
// pattern from auth/user/room services (ADR-0013).
package migrate

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // pgx driver registration
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"vibesync/apps/sync-service/migrations"
)

// sourceFS is the embedded migrations.
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
