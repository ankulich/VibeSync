// Package ports defines the interfaces the Room Service use cases depend on.
package ports

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"vibesync/apps/room-service/internal/domain"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	vboutbox "vibesync/libs/outbox"
)

// RoomRepo is the Room aggregate persistence port.
type RoomRepo interface {
	Create(ctx context.Context, tx pgx.Tx, r domain.Room) error
	GetByID(ctx context.Context, tx pgx.Tx, id string) (domain.Room, error)
	GetBySlug(ctx context.Context, tx pgx.Tx, slug string) (domain.Room, error)
	Update(ctx context.Context, tx pgx.Tx, r domain.Room) error
	Delete(ctx context.Context, tx pgx.Tx, id string) error
	List(ctx context.Context, tx pgx.Tx, cursor string, limit int, search string, visibilities []domain.RoomVisibility) ([]domain.Room, string, error)
}

// MemberRepo is the membership persistence port.
type MemberRepo interface {
	Upsert(ctx context.Context, tx pgx.Tx, m domain.Member) error
	Get(ctx context.Context, tx pgx.Tx, roomID, userID string) (domain.Member, error)
	List(ctx context.Context, tx pgx.Tx, roomID string) ([]domain.Member, error)
	Delete(ctx context.Context, tx pgx.Tx, roomID, userID string) error
	UpdateRole(ctx context.Context, tx pgx.Tx, roomID, userID string, role commonv1.RoomRole) error
	IncrementRoomMemberCount(ctx context.Context, tx pgx.Tx, roomID string, delta int) error
}

// InviteRepo persists invite codes for private/unlisted rooms.
type InviteRepo interface {
	Create(ctx context.Context, tx pgx.Tx, code, roomID string, expiresAt *time.Time) error
	Get(ctx context.Context, tx pgx.Tx, code string) (roomID string, err error)
	Delete(ctx context.Context, tx pgx.Tx, code string) error
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
