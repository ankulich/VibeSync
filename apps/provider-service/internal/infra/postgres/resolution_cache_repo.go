package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/provider-service/internal/domain"
	"vibesync/apps/provider-service/internal/ports"
)

// ResolutionCacheRepo implements ports.ResolutionCacheRepo on resolution_cache.
type ResolutionCacheRepo struct{}

// NewResolutionCacheRepo constructs a ResolutionCacheRepo.
func NewResolutionCacheRepo() *ResolutionCacheRepo { return &ResolutionCacheRepo{} }

// Get loads a cached resolution by (provider, external_ref). The stored row
// does not carry a playable URL; callers rebuild it deterministically from the
// provider + reference. A missing row yields ports.ErrNotFound.
func (ResolutionCacheRepo) Get(ctx context.Context, tx pgx.Tx, provider, externalRef string) (domain.ResolvedMedia, error) {
	var m domain.ResolvedMedia
	err := tx.QueryRow(ctx, `
		SELECT title, artist, cover_url, duration_ms
		FROM resolution_cache
		WHERE provider = $1 AND external_ref = $2`,
		provider, externalRef).
		Scan(&m.Title, &m.Artist, &m.CoverURL, &m.DurationMs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ResolvedMedia{}, ports.NotFound("resolution", provider+":"+externalRef)
		}
		return domain.ResolvedMedia{}, fmt.Errorf("resolution_cache_repo: get: %w", err)
	}
	m.ExternalRef = externalRef
	return m, nil
}

// Upsert inserts or refreshes a resolution_cache row, stamping resolved_at.
func (ResolutionCacheRepo) Upsert(ctx context.Context, tx pgx.Tx, provider string, m domain.ResolvedMedia) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO resolution_cache (provider, external_ref, title, artist, cover_url, duration_ms, resolved_at)
		VALUES ($1,$2,$3,$4,$5,$6, now())
		ON CONFLICT (provider, external_ref) DO UPDATE SET
			title       = EXCLUDED.title,
			artist      = EXCLUDED.artist,
			cover_url   = EXCLUDED.cover_url,
			duration_ms = EXCLUDED.duration_ms,
			resolved_at = now()`,
		provider, m.ExternalRef, m.Title, m.Artist, m.CoverURL, m.DurationMs)
	if err != nil {
		return fmt.Errorf("resolution_cache_repo: upsert: %w", err)
	}
	return nil
}

var _ ports.ResolutionCacheRepo = (*ResolutionCacheRepo)(nil)
