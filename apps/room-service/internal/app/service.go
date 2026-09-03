// Package app implements the Room Service use cases. The Service type satisfies
// roomv1connect.RoomServiceHandler. Mirrors the auth/user-service pattern.
package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/room-service/internal/config"
	"vibesync/apps/room-service/internal/ports"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	vbweb "vibesync/libs/web"
)

// Service is the Room Service use-case facade.
type Service struct {
	cfg    config.Config
	pool   ports.Pool
	rooms  ports.RoomRepo
	members ports.MemberRepo
	invites ports.InviteRepo
	perms   ports.PermissionRepo
	outbox  ports.OutboxWriter
	clock   ports.Clock
	idgen   ports.IDGen
}

// Deps bundles all mandatory port implementations.
type Deps struct {
	Cfg     config.Config
	Pool    ports.Pool
	Rooms   ports.RoomRepo
	Members ports.MemberRepo
	Invites ports.InviteRepo
	Perms   ports.PermissionRepo
	Outbox  ports.OutboxWriter
	Clock   ports.Clock
	IDGen   ports.IDGen
}

// New constructs the Service.
func New(d Deps) *Service {
	if d.Pool == nil || d.Rooms == nil || d.Members == nil || d.Outbox == nil || d.Clock == nil || d.IDGen == nil {
		panic("room/app: all core deps are required")
	}
	return &Service{
		cfg: d.Cfg, pool: d.Pool, rooms: d.Rooms, members: d.Members,
		invites: d.Invites, perms: d.Perms, outbox: d.Outbox, clock: d.Clock, idgen: d.IDGen,
	}
}

func (s *Service) now() time.Time { return s.clock.Now() }

func (s *Service) withTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	tx, err := s.pool.BeginTx(ctx)
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

func (s *Service) readTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return s.withTx(ctx, fn)
}

func ctxDone(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// requestSubject carries the caller's identity from Connect headers.
type requestSubject struct {
	UserID     string
	SystemRole commonv1.SystemRole
}

func subjectFromHeader(h http.Header) requestSubject {
	return requestSubject{
		UserID:     h.Get("X-Vibesync-User-Id"),
		SystemRole: vbweb.ParseSystemRole(h.Get("X-Vibesync-System-Role")),
	}
}

func isNotFound(err error) bool {
	return err != nil && errors.Is(err, ports.ErrNotFound)
}
