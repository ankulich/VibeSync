// Package spotify implements the Spotify Web API adapter for the Provider Service.
package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"vibesync/apps/provider-service/internal/domain"
	"vibesync/apps/provider-service/internal/ports"
)

const (
	// tokenURL is the OAuth token endpoint for the client_credentials grant.
	tokenURL = "https://accounts.spotify.com/api/token"
	// searchURL is the track search endpoint.
	searchURL = "https://api.spotify.com/v1/search"
	// tracksURL is the single-track lookup endpoint prefix.
	tracksURL = "https://api.spotify.com/v1/tracks/"
	// clientCredsCacheKey is the Redis key for the cached app-level token.
	clientCredsCacheKey = "provider-service:spotify:client-credentials-token"
	// tokenCacheMargin is subtracted from the token TTL so cached tokens never
	// expire mid-flight.
	tokenCacheMargin = time.Minute
	// maxBodyBytes caps how much of an API response is read.
	maxBodyBytes = 1 << 20
)

// Cache stores the client-credentials token between calls. Implemented by the
// Redis adapter; nil disables caching.
type Cache interface {
	// Get returns the cached value, or "" when the key is absent.
	Get(ctx context.Context, key string) (string, error)
	// Set stores value under key for ttl.
	Set(ctx context.Context, key, value string, ttl time.Duration) error
}

// Client talks to the Spotify Web API (track search + resolution).
type Client struct {
	http         *http.Client
	clientID     string
	clientSecret string
	cache        Cache
}

// NewClient constructs the Spotify adapter. cache may be nil.
func NewClient(httpClient *http.Client, clientID, clientSecret string, cache Cache) *Client {
	return &Client{http: httpClient, clientID: clientID, clientSecret: clientSecret, cache: cache}
}

// artistRef is the artist stub embedded in track payloads.
type artistRef struct {
	Name string `json:"name"`
}

// imageRef is one album-cover size variant.
type imageRef struct {
	URL string `json:"url"`
}

// albumRef is the album stub embedded in track payloads.
type albumRef struct {
	Images []imageRef `json:"images"`
}

// track is a track object from the Spotify Web API.
type track struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Artists    []artistRef `json:"artists"`
	Album      albumRef    `json:"album"`
	DurationMS int64       `json:"duration_ms"`
}

// searchResponse is the /v1/search envelope.
type searchResponse struct {
	Tracks struct {
		Items []track `json:"items"`
	} `json:"tracks"`
}

// tokenResponse is the client_credentials token payload.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// getClientCredentialsToken fetches an app-level access token via the
// client_credentials grant, serving it from the cache when possible.
func (c *Client) getClientCredentialsToken(ctx context.Context) (string, error) {
	if c.cache != nil {
		if tok, err := c.cache.Get(ctx, clientCredsCacheKey); err == nil && tok != "" {
			return tok, nil
		}
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("spotify: token request: %w", err)
	}
	req.SetBasicAuth(c.clientID, c.clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("spotify: token request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", fmt.Errorf("spotify: token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("spotify: token: status %d: %s", resp.StatusCode, snippet(body))
	}
	var parsed tokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("spotify: token decode: %w", err)
	}
	if parsed.AccessToken == "" {
		return "", fmt.Errorf("spotify: token: empty access_token")
	}
	if c.cache != nil {
		ttl := time.Duration(parsed.ExpiresIn)*time.Second - tokenCacheMargin
		if ttl < time.Minute {
			ttl = time.Minute
		}
		// Token caching is best-effort; a failed write only costs a re-fetch.
		_ = c.cache.Set(ctx, clientCredsCacheKey, parsed.AccessToken, ttl)
	}
	return parsed.AccessToken, nil
}

// Search queries Spotify tracks with the app-level token and maps the top hits
// to domain search results.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]domain.SearchResult, error) {
	token, err := c.getClientCredentialsToken(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{"q": {query}, "type": {"track"}, "limit": {strconv.Itoa(limit)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("spotify: search request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("spotify: search request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("spotify: search response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("spotify: search: status %d: %s", resp.StatusCode, snippet(body))
	}
	var parsed searchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("spotify: search decode: %w", err)
	}
	results := make([]domain.SearchResult, 0, len(parsed.Tracks.Items))
	for _, it := range parsed.Tracks.Items {
		results = append(results, domain.SearchResult{
			ExternalRef: it.ID,
			Title:       it.Name,
			Artist:      artistName(it.Artists),
			CoverURL:    coverURL(it.Album.Images),
			DurationMs:  it.DurationMS,
		})
	}
	return results, nil
}

// Resolve fetches full track metadata. A non-empty userToken (per-user OAuth
// token from the Auth Service) is preferred; otherwise the app-level
// client-credentials token is used.
func (c *Client) Resolve(ctx context.Context, externalRef, userToken string) (domain.ResolvedMedia, error) {
	token := userToken
	if token == "" {
		var err error
		token, err = c.getClientCredentialsToken(ctx)
		if err != nil {
			return domain.ResolvedMedia{}, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tracksURL+url.PathEscape(externalRef), nil)
	if err != nil {
		return domain.ResolvedMedia{}, fmt.Errorf("spotify: track request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return domain.ResolvedMedia{}, fmt.Errorf("spotify: track request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return domain.ResolvedMedia{}, fmt.Errorf("spotify: track response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return domain.ResolvedMedia{}, ports.NotFound("spotify track", externalRef)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.ResolvedMedia{}, fmt.Errorf("spotify: track: status %d: %s", resp.StatusCode, snippet(body))
	}
	var t track
	if err := json.Unmarshal(body, &t); err != nil {
		return domain.ResolvedMedia{}, fmt.Errorf("spotify: track decode: %w", err)
	}
	if t.ID == "" {
		return domain.ResolvedMedia{}, ports.NotFound("spotify track", externalRef)
	}
	return domain.ResolvedMedia{
		ExternalRef: t.ID,
		Title:       t.Name,
		Artist:      artistName(t.Artists),
		CoverURL:    coverURL(t.Album.Images),
		PlayableURL: "https://open.spotify.com/track/" + t.ID,
		DurationMs:  t.DurationMS,
	}, nil
}

// artistName returns the primary artist, or "" when the list is empty.
func artistName(artists []artistRef) string {
	if len(artists) == 0 {
		return ""
	}
	return artists[0].Name
}

// coverURL returns the largest album cover, or "" when there are none.
func coverURL(images []imageRef) string {
	if len(images) == 0 {
		return ""
	}
	return images[0].URL
}

// snippet returns a short excerpt of an API error body for error messages.
func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

var _ ports.SpotifyProvider = (*Client)(nil)
