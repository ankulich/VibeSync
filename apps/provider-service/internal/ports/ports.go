// Package ports defines the interfaces the Provider Service use cases depend on.
package ports

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"vibesync/apps/provider-service/internal/domain"
)

// ErrSearchUnsupported is returned by providers that have no keyless search
// surface (e.g. YouTube without the Data API — see ADR-0016). Callers map it
// to a typed Unimplemented error; clients add such media by URL instead.
var ErrSearchUnsupported = errors.New("provider: search is not supported for this provider")

// ExternalProvider is the search/resolve surface of an external music provider.
type ExternalProvider interface {
	Search(ctx context.Context, query string, limit int) ([]domain.SearchResult, error)
	Resolve(ctx context.Context, externalRef string) (domain.ResolvedMedia, error)
}

// SpotifyProvider is the Spotify-specific surface. Resolve additionally
// accepts an optional per-user OAuth token (fetched from the Auth Service) so
// results reflect the caller's account when one is linked.
type SpotifyProvider interface {
	Search(ctx context.Context, query string, limit int) ([]domain.SearchResult, error)
	Resolve(ctx context.Context, externalRef, userToken string) (domain.ResolvedMedia, error)
}

// ResolutionCacheRepo is the resolution cache persistence port.
type ResolutionCacheRepo interface {
	Get(ctx context.Context, tx pgx.Tx, provider, externalRef string) (domain.ResolvedMedia, error)
	Upsert(ctx context.Context, tx pgx.Tx, provider string, m domain.ResolvedMedia) error
}

// TokenSource fetches per-user provider access tokens from the Auth Service.
type TokenSource interface {
	GetUserToken(ctx context.Context, userID, provider string) (string, error)
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

// notFoundErr is the canonical not-found sentinel implementation.
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
