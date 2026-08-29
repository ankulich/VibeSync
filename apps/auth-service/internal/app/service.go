// Package app implements the Auth use cases. The Service type satisfies
// authv1connect.AuthServiceHandler and wires the domain logic to the ports.
//
// Each use case lives in its own file (login.go, refresh.go, oauth.go, etc.)
// for readability; this file holds the shared Service struct + constructor.
//
// All use cases that mutate state run inside a Postgres transaction so the
// domain write and the outbox event stage atomically. The withTx helper
// centralizes begin/commit/rollback.
package app

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/auth-service/internal/config"
	"vibesync/apps/auth-service/internal/ports"
)

// Service is the Auth use-case facade. It implements authv1connect.
// AuthServiceHandler. Each method is a use case; helpers are private.
type Service struct {
	cfg config.Config

	pool     ports.Pool
	users    ports.UserRepo
	sessions ports.SessionRepo
	refresh  ports.RefreshTokenRepo
	keys     ports.SigningKeyRepo
	flows    ports.OAuthFlowRepo
	accounts ports.OAuthAccountRepo
	outbox   ports.OutboxWriter

	hasher   ports.PasswordHasher
	cipher   ports.KeyCipher
	signer   ports.TokenSigner
	registry oauthRegistry

	clock ports.Clock
	idgen ports.IDGen
}

// oauthRegistry is the local interface the use case needs. The infra/oauth
// Registry implements it; defining it here keeps app from depending on the
// oauth infra package.
type oauthRegistry interface {
	Get(name string) (ports.OAuthProvider, bool)
}

// Deps bundles all the port implementations the Service constructor takes.
// Explicit (rather than variadic options) because every dep is mandatory and
// missing one is a startup bug, not a tunable.
type Deps struct {
	Cfg      config.Config
	Pool     ports.Pool
	Users    ports.UserRepo
	Sessions ports.SessionRepo
	Refresh  ports.RefreshTokenRepo
	Keys     ports.SigningKeyRepo
	Flows    ports.OAuthFlowRepo
	Accounts ports.OAuthAccountRepo
	Outbox   ports.OutboxWriter
	Hasher   ports.PasswordHasher
	Cipher   ports.KeyCipher
	Signer   ports.TokenSigner
	Registry oauthRegistry
	Clock    ports.Clock
	IDGen    ports.IDGen
}

// New constructs the Service. Core deps are mandatory; OAuth deps are optional
// (a service with no providers configured can still do password auth). Missing
// core deps panic so startup failures are loud and early.
func New(d Deps) *Service {
	if d.Pool == nil || d.Users == nil || d.Sessions == nil || d.Refresh == nil ||
		d.Keys == nil || d.Outbox == nil || d.Hasher == nil || d.Signer == nil ||
		d.Clock == nil || d.IDGen == nil {
		panic("auth/app: all core deps are required")
	}
	return &Service{
		cfg:      d.Cfg,
		pool:     d.Pool,
		users:    d.Users,
		sessions: d.Sessions,
		refresh:  d.Refresh,
		keys:     d.Keys,
		flows:    d.Flows,
		accounts: d.Accounts,
		outbox:   d.Outbox,
		hasher:   d.Hasher,
		cipher:   d.Cipher,
		signer:   d.Signer,
		registry: d.Registry,
		clock:    d.Clock,
		idgen:    d.IDGen,
	}
}

// now returns the current time via the injected Clock. Centralized so use cases
// don't reach for time.Now directly — that would break time-pinned tests.
func (s *Service) now() time.Time { return s.clock.Now() }

// withTx runs fn inside a Postgres transaction. On nil return the tx commits;
// on error or panic the tx rolls back. This is the ONLY sanctioned way to run
// multi-statement mutations in Auth, mirroring libs/store.InTx.
//
// pgx.Tx is threaded through directly (rather than a store.Tx abstraction)
// because our repos take pgx.Tx; an abstraction layer would only add an
// adapter for no benefit.
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

// readTx returns a tx suitable for read-only use. For now this is a regular
// tx (Postgres doesn't have a meaningful read-only tx mode without extra
// config); the caller still commits (a no-op write) or rolls back. The
// explicit helper exists so the call site documents intent.
func (s *Service) readTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return s.withTx(ctx, fn)
}

// ctxDone returns ctx.Err() if the context is already cancelled, else nil.
// Use as the first line of every handler so a cancelled request fails fast
// before any work.
func ctxDone(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
