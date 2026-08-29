// Package ports defines the interfaces the Sync Service use cases depend on.
package ports

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"vibesync/apps/sync-service/internal/domain"
	vboutbox "vibesync/libs/outbox"
)

// SyncStateRepo persists authoritative playback state for restart recovery.
type SyncStateRepo interface {
	Upsert(ctx context.Context, tx pgx.Tx, s domain.SyncState) error
	Get(ctx context.Context, tx pgx.Tx, roomID string) (domain.SyncState, error)
}

// OutboxWriter stages events in the same tx as the domain write.
type OutboxWriter interface {
	Append(ctx context.Context, tx pgx.Tx, events ...vboutbox.Event) error
}

// OutboxStager stages a single event in its own transaction. Used by the
// RoomSync background loop, which doesn't have direct pool access but needs
// to stage sync.updated.v1 events. The concrete implementation wraps the pool.
type OutboxStager func(ctx context.Context, event vboutbox.Event) error

// Pool is the connection-pool + tx-runner surface.
type Pool interface {
	BeginTx(ctx context.Context) (pgx.Tx, error)
	Pool() *pgxpool.Pool
	Close()
}

// Presence tracks which users are active in a room (for host-migration
// successor selection and liveness).
type Presence interface {
	Join(ctx context.Context, roomID, userID string) error
	Heartbeat(ctx context.Context, roomID, userID string) error
	Leave(ctx context.Context, roomID, userID string) error
	Active(ctx context.Context, roomID string, within time.Duration) ([]string, error)
}

// Clock returns the current time.
type Clock interface {
	Now() time.Time
	NowMs() int64
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
