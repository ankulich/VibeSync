// Package ports defines the interfaces the Media Service use cases depend on.
package ports

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"vibesync/apps/media-service/internal/domain"
	vboutbox "vibesync/libs/outbox"
)

// MediaRepo is the media catalog persistence port.
type MediaRepo interface {
	Create(ctx context.Context, tx pgx.Tx, m domain.Media) error
	GetByID(ctx context.Context, tx pgx.Tx, id string) (domain.Media, error)
	List(ctx context.Context, tx pgx.Tx, cursor string, limit int, search string) ([]domain.Media, string, error)
}

// QueueRepo is the per-room queue persistence port.
type QueueRepo interface {
	Add(ctx context.Context, tx pgx.Tx, roomID, mediaID string) (domain.QueueItem, error)
	List(ctx context.Context, tx pgx.Tx, roomID string) ([]domain.QueueItem, error)
	Remove(ctx context.Context, tx pgx.Tx, roomID string, position int) error
}

// OutboxWriter stages events in the same tx as the domain write.
type OutboxWriter interface {
	Append(ctx context.Context, tx pgx.Tx, events ...vboutbox.Event) error
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
