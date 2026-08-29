// Package ports defines the interfaces the User Service use cases depend on.
// Mirrors the auth-service pattern: each port is implemented by an adapter in
// internal/infra, so use cases are testable in isolation.
package ports

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"vibesync/apps/user-service/internal/domain"
)

// UserRepo is the User read-model persistence port.
type UserRepo interface {
	// Upsert inserts a new user or updates an existing one (by ID). Idempotent:
	// safe to call on redelivered events (INSERT ... ON CONFLICT DO UPDATE).
	Upsert(ctx context.Context, tx pgx.Tx, u domain.User) error
	// GetByID loads a user by primary key. Not-found returns ErrNotFound.
	GetByID(ctx context.Context, tx pgx.Tx, id string) (domain.User, error)
	// Update writes the mutable fields of a user (display_name, avatar_url,
	// updated_at). Identity fields (email, username, role) are NOT updatable
	// here — they belong to Auth.
	Update(ctx context.Context, tx pgx.Tx, u domain.User) error
	// List returns a page of users ordered by created_at DESC. Optional search
	// filters by username (trigram fuzzy match). cursor is a ULID for
	// keyset pagination (users with created_at strictly before the cursor's
	// time). Returns the page + the next cursor (empty if no more).
	List(ctx context.Context, tx pgx.Tx, cursor string, limit int, search string) ([]domain.User, string, error)
}

// Pool is the connection-pool + tx-runner surface.
type Pool interface {
	BeginTx(ctx context.Context) (pgx.Tx, error)
	Pool() *pgxpool.Pool
	Close()
}

// Clock returns the current time. Injected for testability.
type Clock interface {
	Now() time.Time
}

// ErrNotFound is the canonical not-found sentinel returned by repositories.
type notFoundErr struct{ entity, id string }

// NotFound constructs a typed not-found error.
func NotFound(entity, id string) error { return notFoundErr{entity, id} }

func (e notFoundErr) Error() string { return e.entity + " not found: " + e.id }

// ErrNotFound is the sentinel compared via errors.Is.
var ErrNotFound = notFoundErr{}

func (notFoundErr) Is(target error) bool {
	_, ok := target.(notFoundErr)
	return ok
}
