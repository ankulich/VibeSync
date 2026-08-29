// Package app implements the User Service use cases: a Kafka consumer handler
// that projects user.created.v1 events into the read model, and the three
// UserService Connect handlers (GetUser, UpdateUser, ListUsers).
//
// The Service struct mirrors the auth-service pattern: explicit Deps, withTx
// for atomicity, ctxDone for fast-fail on cancelled requests.
package app

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/user-service/internal/config"
	"vibesync/apps/user-service/internal/ports"
)

// Service is the User Service use-case facade. Implements
// userv1connect.UserServiceHandler.
type Service struct {
	cfg   config.Config
	pool  ports.Pool
	users ports.UserRepo
	clock ports.Clock
}

// Deps bundles all mandatory port implementations.
type Deps struct {
	Cfg   config.Config
	Pool  ports.Pool
	Users ports.UserRepo
	Clock ports.Clock
}

// New constructs the Service. Core deps are mandatory; missing ones panic.
func New(d Deps) *Service {
	if d.Pool == nil || d.Users == nil || d.Clock == nil {
		panic("user/app: all core deps are required")
	}
	return &Service{
		cfg:   d.Cfg,
		pool:  d.Pool,
		users: d.Users,
		clock: d.Clock,
	}
}

// now returns the current time via the injected Clock.
func (s *Service) now() time.Time { return s.clock.Now() }

// withTx runs fn inside a Postgres transaction. On nil return → commit;
// on error/panic → rollback. Mirrors auth-service's withTx.
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

// readTx is a thin alias that documents read-only intent.
func (s *Service) readTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return s.withTx(ctx, fn)
}

// ctxDone returns ctx.Err() if the context is already cancelled, else nil.
func ctxDone(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
