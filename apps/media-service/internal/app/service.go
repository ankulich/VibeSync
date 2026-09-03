// Package app implements the Media Service use cases. The Service type satisfies
// mediav1connect.MediaServiceHandler. Mirrors the room-service pattern.
package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/media-service/internal/config"
	"vibesync/apps/media-service/internal/ports"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	vbweb "vibesync/libs/web"
)

// Service is the Media Service use-case facade.
type Service struct {
	cfg    config.Config
	pool   ports.Pool
	media  ports.MediaRepo
	queue  ports.QueueRepo
	perms  ports.RoomPermissions
	outbox ports.OutboxWriter
	clock  ports.Clock
	idgen  ports.IDGen
}

// Deps bundles all mandatory port implementations.
type Deps struct {
	Cfg    config.Config
	Pool   ports.Pool
	Media  ports.MediaRepo
	Queue  ports.QueueRepo
	Perms  ports.RoomPermissions
	Outbox ports.OutboxWriter
	Clock  ports.Clock
	IDGen  ports.IDGen
}

// New constructs the Service.
func New(d Deps) *Service {
	if d.Pool == nil || d.Media == nil || d.Queue == nil || d.Outbox == nil || d.Clock == nil || d.IDGen == nil {
		panic("media/app: all core deps are required")
	}
	if d.Perms == nil {
		panic("media/app: room permissions port is required")
	}
	return &Service{
		cfg: d.Cfg, pool: d.Pool, media: d.Media, queue: d.Queue,
		perms: d.Perms, outbox: d.Outbox, clock: d.Clock, idgen: d.IDGen,
	}
}

// now returns the current time from the configured clock.
func (s *Service) now() time.Time { return s.clock.Now() }

// withTx runs fn inside a pool transaction, committing on nil error and
// rolling back (and repanicking) otherwise.
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

// readTx runs fn inside a transaction. Read paths reuse withTx for a single
// snapshot-consistent statement batch.
func (s *Service) readTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return s.withTx(ctx, fn)
}

// ctxDone returns the context error if it has been cancelled.
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

// subjectFromHeader extracts the caller's identity from Connect headers.
func subjectFromHeader(h http.Header) requestSubject {
	return requestSubject{
		UserID:     h.Get("X-Vibesync-User-Id"),
		SystemRole: vbweb.ParseSystemRole(h.Get("X-Vibesync-System-Role")),
	}
}

// isNotFound reports whether err is the canonical ports.ErrNotFound.
func isNotFound(err error) bool {
	return err != nil && errors.Is(err, ports.ErrNotFound)
}
