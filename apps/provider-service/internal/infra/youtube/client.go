// Package youtube implements the keyless YouTube adapter for the Provider
// Service. Metadata comes from the public oEmbed endpoint; playback happens
// in the YouTube IFrame player on the frontend. The YouTube Data API is
// deliberately not used (no API key, no quota — see ADR-0016), which means
// there is no server-side search: clients add videos by URL instead.
package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"vibesync/apps/provider-service/internal/domain"
	"vibesync/apps/provider-service/internal/ports"
)

const (
	// defaultBaseURL is the YouTube origin used for oEmbed lookups.
	defaultBaseURL = "https://www.youtube.com"
	// maxBodyBytes caps how much of an oEmbed response is read.
	maxBodyBytes = 1 << 20
)

// Client resolves YouTube video metadata through the public oEmbed endpoint.
type Client struct {
	http    *http.Client
	baseURL string
}

// NewClient constructs the YouTube adapter.
func NewClient(httpClient *http.Client) *Client {
	return newClient(httpClient, defaultBaseURL)
}

// newClient constructs the adapter with an explicit origin (tests).
func newClient(httpClient *http.Client, baseURL string) *Client {
	return &Client{http: httpClient, baseURL: baseURL}
}

// oembedResponse is the public oEmbed payload for a video. The endpoint is
// keyless and returns title, channel and thumbnail; it has no duration —
// the IFrame player reports that on the client.
type oembedResponse struct {
	Title        string `json:"title"`
	AuthorName   string `json:"author_name"`
	ThumbnailURL string `json:"thumbnail_url"`
}

// Search is not supported: the keyless oEmbed surface has no search, and the
// Data API is out of scope by design.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]domain.SearchResult, error) {
	return nil, ports.ErrSearchUnsupported
}

// Resolve fetches video metadata (title, channel, thumbnail) via oEmbed.
// DurationMs is zero: oEmbed does not carry it.
func (c *Client) Resolve(ctx context.Context, externalRef string) (domain.ResolvedMedia, error) {
	q := url.Values{
		"url":    {"https://www.youtube.com/watch?v=" + externalRef},
		"format": {"json"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/oembed?"+q.Encode(), nil)
	if err != nil {
		return domain.ResolvedMedia{}, fmt.Errorf("youtube: request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return domain.ResolvedMedia{}, fmt.Errorf("youtube: request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return domain.ResolvedMedia{}, fmt.Errorf("youtube: response: %w", err)
	}
	// oEmbed answers 400/401/403/404 for anything that is not a public,
	// embeddable video (missing, private, deleted, embedding disabled).
	if resp.StatusCode == http.StatusBadRequest ||
		resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusNotFound {
		return domain.ResolvedMedia{}, ports.NotFound("youtube video", externalRef)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.ResolvedMedia{}, fmt.Errorf("youtube: status %d: %s", resp.StatusCode, snippet(body))
	}
	var parsed oembedResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return domain.ResolvedMedia{}, fmt.Errorf("youtube: decode: %w", err)
	}
	return domain.ResolvedMedia{
		ExternalRef: externalRef,
		Title:       parsed.Title,
		Artist:      parsed.AuthorName,
		CoverURL:    parsed.ThumbnailURL,
		PlayableURL: "https://www.youtube.com/watch?v=" + externalRef,
		DurationMs:  0,
	}, nil
}

// snippet returns a short excerpt of an API error body for error messages.
func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

var _ ports.ExternalProvider = (*Client)(nil)
