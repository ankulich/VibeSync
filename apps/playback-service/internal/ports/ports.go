// Package ports defines the interfaces the Playback Service use cases depend on.
package ports

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"vibesync/apps/playback-service/internal/domain"
)

// PlaybackRepo persists the cached playback state for restart recovery.
type PlaybackRepo interface {
	Upsert(ctx context.Context, tx pgx.Tx, r domain.PlaybackRoom) error
	Get(ctx context.Context, tx pgx.Tx, roomID string) (domain.PlaybackRoom, error)
}

// Pool is the connection-pool + tx-runner surface.
type Pool interface {
	BeginTx(ctx context.Context) (pgx.Tx, error)
	Pool() *pgxpool.Pool
	Close()
}

// Clock returns the current time.
type Clock interface {
	Now() time.Time
}

// IDGen generates canonical ULIDs.
type IDGen interface {
	New() string
}

// ErrNotFound is the canonical not-found sentinel.
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
