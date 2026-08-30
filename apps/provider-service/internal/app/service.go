// Package app implements the Provider Service use cases. The Service type
// satisfies providerv1connect.ProviderServiceHandler. Mirrors the media-service
// pattern.
package app

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/provider-service/internal/config"
	"vibesync/apps/provider-service/internal/ports"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	vbweb "vibesync/libs/web"
)

// Service is the Provider Service use-case facade.
type Service struct {
	cfg       config.Config
	pool      ports.Pool
	cacheRepo ports.ResolutionCacheRepo
	spotify   ports.SpotifyProvider
	youtube   ports.ExternalProvider
	tokens    ports.TokenSource
	clock     ports.Clock
}

// Deps bundles all port implementations. Spotify and YouTube may be nil when
// the corresponding provider is disabled in config; the related RPCs then fail
// with FailedPrecondition.
type Deps struct {
	Cfg       config.Config
	Pool      ports.Pool
	CacheRepo ports.ResolutionCacheRepo
	Spotify   ports.SpotifyProvider
	YouTube   ports.ExternalProvider
	Tokens    ports.TokenSource
	Clock     ports.Clock
}

// New constructs the Service.
func New(d Deps) *Service {
	if d.Pool == nil || d.CacheRepo == nil || d.Tokens == nil || d.Clock == nil {
		panic("provider/app: all core deps are required")
	}
	return &Service{
		cfg: d.Cfg, pool: d.Pool, cacheRepo: d.CacheRepo,
		spotify: d.Spotify, youtube: d.YouTube,
		tokens: d.Tokens, clock: d.Clock,
	}
}

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
