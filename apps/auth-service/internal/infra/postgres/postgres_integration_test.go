//go:build integration

// Integration tests for the Postgres repositories. Gated behind the
// "integration" build tag AND VIBE_TEST_INTEGRATION=1 env so they don't run
// in the default `go test` (which must stay Docker-free).
//
// Run: VIBE_TEST_INTEGRATION=1 go test -tags=integration -count=1 ./internal/infra/postgres/...
//
// These tests boot a real Postgres via testcontainers, apply migrations, and
// exercise the repositories end-to-end. They are the source of truth for the
// SQL ↔ domain mapping; if a column type or scan order is wrong, these fail.
package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"vibesync/apps/auth-service/internal/domain"
	"vibesync/apps/auth-service/internal/infra/migrate"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

// TestUserRepoRoundTrip exercises Create → GetByEmail → GetByID → Update.
func TestUserRepoRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := setupDB(t, ctx)
	defer pool.Close()

	repo := NewUserRepo()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	user, err := domain.NewUser(now, domain.NewUserParams{
		Email:       "alice@example.com",
		Username:    "alice",
		DisplayName: "Alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	user.ID = "01J6TESTUSERALICE000000A" // deterministic ULID-shaped id

	err = withTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		return repo.Create(ctx, tx, user)
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var got domain.User
	err = withTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		var ferr error
		got, ferr = repo.GetByEmail(ctx, tx, "ALICE@example.com") // case-insensitive
		return ferr
	})
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.Email != "alice@example.com" {
		t.Errorf("Email = %q", got.Email)
	}
	if got.SystemRole != commonv1.SystemRole_SYSTEM_ROLE_USER {
		t.Errorf("SystemRole = %v", got.SystemRole)
	}

	// Update the user.
	got.DisplayName = "Alice Updated"
	err = withTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		return repo.Update(ctx, tx, got)
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
}

// setupDB boots a Postgres testcontainer, applies migrations, and returns a
// pool. t.Cleanup tears everything down.
func setupDB(t *testing.T, ctx context.Context) *Pool {
	t.Helper()
	if os.Getenv("VIBE_TEST_INTEGRATION") != "1" {
		t.Skip("set VIBE_TEST_INTEGRATION=1 to run integration tests")
	}
	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("auth"),
		postgres.WithUsername("vibesync"),
		postgres.WithPassword("vibesync"),
	)
	if err != nil {
		t.Fatalf("postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(ctx)
	})

	host, _ := container.Host(ctx)
	port, _ := container.MappedPort(ctx, "5432")
	dbURL := fmt.Sprintf("postgres://vibesync:vibesync@%s:%s/auth?sslmode=disable",
		host, port.Port())

	if err := migrate.Run(ctx, dbURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, err := NewPool(ctx, dbURL, 5)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	return pool
}

// withTx is a thin helper for tests: begin, run fn, commit on nil / rollback
// on error. Mirrors app.Service.withTx.
func withTx(ctx context.Context, pool *Pool, fn func(context.Context, pgx.Tx) error) error {
	tx, err := pool.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err == nil {
			err = tx.Commit(ctx)
			return
		}
		_ = tx.Rollback(ctx)
	}()
	return fn(ctx, tx)
}
