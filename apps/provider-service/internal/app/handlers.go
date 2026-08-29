package app

import (
	"context"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	"vibesync/apps/provider-service/internal/domain"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	providerv1 "vibesync/gen/go/vibesync/provider/v1"
	providerv1connect "vibesync/gen/go/vibesync/provider/v1/providerv1connect"
	vberr "vibesync/libs/errors"
)

// Search limits, clamped to what both provider APIs accept.
const (
	defaultSearchLimit = 20
	maxSearchLimit     = 50
)

// providerSearcher is the shared search surface of both provider adapters.
type providerSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]domain.SearchResult, error)
}

// Search dispatches a query to the requested external provider and returns its
// hits.
func (s *Service) Search(ctx context.Context, req *connect.Request[providerv1.SearchRequest]) (*connect.Response[providerv1.SearchResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	query := req.Msg.GetQuery()
	if query == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.provider", "MISSING_QUERY", "query is required")
	}
	searcher, err := s.searchProvider(req.Msg.GetProvider())
	if err != nil {
		return nil, err
	}
	results, err := searcher.Search(ctx, query, searchLimit(req.Msg.GetPage()))
	if err != nil {
		return nil, vberr.Internal("SEARCH_FAILED", err.Error()).WithCause(err)
	}
	protos := make([]*providerv1.SearchResult, 0, len(results))
	for _, r := range results {
		protos = append(protos, searchResultToProto(r))
	}
	return connect.NewResponse(&providerv1.SearchResponse{
		Results: protos,
		Page:    &commonv1.PageResponse{Total: uint64(len(protos))},
	}), nil
}

// Resolve returns full metadata for an external media reference, serving from
// the resolution cache on hit and the provider on miss.
func (s *Service) Resolve(ctx context.Context, req *connect.Request[providerv1.ResolveRequest]) (*connect.Response[providerv1.ResolveResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	externalRef := req.Msg.GetExternalRef()
	if externalRef == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.provider", "MISSING_EXTERNAL_REF", "external_ref is required")
	}
	provider := req.Msg.GetProvider()
	slug, err := providerSlug(provider)
	if err != nil {
		return nil, err
	}

	// Cache hit: rebuild the deterministic playable URL and return.
	var cached domain.ResolvedMedia
	err = s.readTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var ferr error
		cached, ferr = s.cacheRepo.Get(ctx, tx, slug, externalRef)
		return ferr
	})
	if err != nil && !isNotFound(err) {
		return nil, vberr.Internal("RESOLVE_CACHE_READ_FAILED", err.Error()).WithCause(err)
	}
	if err == nil {
		return connect.NewResponse(resolveResponse(fillPlayableURL(provider, cached))), nil
	}

	// Cache miss: resolve against the provider.
	var media domain.ResolvedMedia
	switch provider {
	case providerv1.ProviderName_PROVIDER_NAME_SPOTIFY:
		if s.spotify == nil {
			return nil, vberr.FailedPrecondition("vibesync.provider", "PROVIDER_DISABLED", "spotify provider is not enabled")
		}
		userToken := ""
		if subject := subjectFromHeader(req.Header()); subject.UserID != "" {
			userToken, err = s.tokens.GetUserToken(ctx, subject.UserID, slug)
			if err != nil {
				return nil, vberr.Internal("USER_TOKEN_FETCH_FAILED", err.Error()).WithCause(err)
			}
		}
		media, err = s.spotify.Resolve(ctx, externalRef, userToken)
	case providerv1.ProviderName_PROVIDER_NAME_YOUTUBE:
		if s.youtube == nil {
			return nil, vberr.FailedPrecondition("vibesync.provider", "PROVIDER_DISABLED", "youtube provider is not enabled")
		}
		media, err = s.youtube.Resolve(ctx, externalRef)
	}
	if err != nil {
		if isNotFound(err) {
			return nil, vberr.NotFound(slug+" media", externalRef)
		}
		return nil, vberr.Internal("RESOLVE_FAILED", err.Error()).WithCause(err)
	}

	// Cache the fresh resolution before returning it.
	if err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return s.cacheRepo.Upsert(ctx, tx, slug, media)
	}); err != nil {
		return nil, vberr.Internal("RESOLVE_CACHE_WRITE_FAILED", err.Error()).WithCause(err)
	}
	return connect.NewResponse(resolveResponse(fillPlayableURL(provider, media))), nil
}

// searchProvider returns the search adapter for the requested provider.
func (s *Service) searchProvider(p providerv1.ProviderName) (providerSearcher, error) {
	switch p {
	case providerv1.ProviderName_PROVIDER_NAME_SPOTIFY:
		if s.spotify == nil {
			return nil, vberr.FailedPrecondition("vibesync.provider", "PROVIDER_DISABLED", "spotify provider is not enabled")
		}
		return s.spotify, nil
	case providerv1.ProviderName_PROVIDER_NAME_YOUTUBE:
		if s.youtube == nil {
			return nil, vberr.FailedPrecondition("vibesync.provider", "PROVIDER_DISABLED", "youtube provider is not enabled")
		}
		return s.youtube, nil
	default:
		return nil, vberr.InvalidArgumentFor("vibesync.provider", "INVALID_PROVIDER", "provider must be PROVIDER_NAME_SPOTIFY or PROVIDER_NAME_YOUTUBE")
	}
}

// providerSlug maps the ProviderName enum to the canonical cache-key slug.
func providerSlug(p providerv1.ProviderName) (string, error) {
	switch p {
	case providerv1.ProviderName_PROVIDER_NAME_SPOTIFY:
		return "spotify", nil
	case providerv1.ProviderName_PROVIDER_NAME_YOUTUBE:
		return "youtube", nil
	default:
		return "", vberr.InvalidArgumentFor("vibesync.provider", "INVALID_PROVIDER", "provider must be PROVIDER_NAME_SPOTIFY or PROVIDER_NAME_YOUTUBE")
	}
}

// searchLimit resolves the effective page size, clamped to [1, maxSearchLimit].
func searchLimit(page *commonv1.PageRequest) int {
	limit := defaultSearchLimit
	if l := page.GetLimit(); l > 0 {
		limit = int(l)
	}
	if limit > maxSearchLimit {
		return maxSearchLimit
	}
	return limit
}

// fillPlayableURL rebuilds the provider's canonical playable URL when the
// (cached) media does not carry one.
func fillPlayableURL(p providerv1.ProviderName, m domain.ResolvedMedia) domain.ResolvedMedia {
	if m.PlayableURL != "" {
		return m
	}
	switch p {
	case providerv1.ProviderName_PROVIDER_NAME_SPOTIFY:
		m.PlayableURL = "https://open.spotify.com/track/" + m.ExternalRef
	case providerv1.ProviderName_PROVIDER_NAME_YOUTUBE:
		m.PlayableURL = "https://www.youtube.com/watch?v=" + m.ExternalRef
	}
	return m
}

// searchResultToProto converts a domain search hit to its proto form.
func searchResultToProto(r domain.SearchResult) *providerv1.SearchResult {
	return &providerv1.SearchResult{
		ExternalRef: r.ExternalRef,
		Title:       r.Title,
		Artist:      r.Artist,
		CoverUrl:    r.CoverURL,
		DurationMs:  uint64(r.DurationMs),
	}
}

// resolveResponse converts resolved media to the ResolveResponse proto.
func resolveResponse(m domain.ResolvedMedia) *providerv1.ResolveResponse {
	return &providerv1.ResolveResponse{
		Title:       m.Title,
		Artist:      m.Artist,
		CoverUrl:    m.CoverURL,
		DurationMs:  uint64(m.DurationMs),
		PlayableUrl: m.PlayableURL,
	}
}

// Compile-time assertion that *Service satisfies the full handler interface.
var _ providerv1connect.ProviderServiceHandler = (*Service)(nil)
