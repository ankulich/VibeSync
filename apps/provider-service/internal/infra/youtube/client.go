// Package youtube implements the YouTube Data API v3 adapter for the Provider Service.
package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"vibesync/apps/provider-service/internal/domain"
	"vibesync/apps/provider-service/internal/ports"
)

const (
	// searchEndpoint is the v3 search.list endpoint.
	searchEndpoint = "https://www.googleapis.com/youtube/v3/search"
	// videosEndpoint is the v3 videos.list endpoint.
	videosEndpoint = "https://www.googleapis.com/youtube/v3/videos"
	// maxBodyBytes caps how much of an API response is read.
	maxBodyBytes = 1 << 20
)

// Client talks to the YouTube Data API v3 (video search + resolution) with an
// API key.
type Client struct {
	http   *http.Client
	apiKey string
}

// NewClient constructs the YouTube adapter.
func NewClient(httpClient *http.Client, apiKey string) *Client {
	return &Client{http: httpClient, apiKey: apiKey}
}

// thumbnailSet is the thumbnails map on a snippet.
type thumbnailSet struct {
	High struct {
		URL string `json:"url"`
	} `json:"high"`
}

// videoSnippet is the metadata part of a video resource.
type videoSnippet struct {
	Title        string       `json:"title"`
	ChannelTitle string       `json:"channelTitle"`
	Thumbnails   thumbnailSet `json:"thumbnails"`
}

// searchItem is one result row from the search endpoint.
type searchItem struct {
	ID struct {
		VideoID string `json:"videoId"`
	} `json:"id"`
	Snippet videoSnippet `json:"snippet"`
}

// searchResponse is the search endpoint envelope.
type searchResponse struct {
	Items []searchItem `json:"items"`
}

// videoItem is one row from the videos endpoint.
type videoItem struct {
	ID             string       `json:"id"`
	Snippet        videoSnippet `json:"snippet"`
	ContentDetails struct {
		Duration string `json:"duration"`
	} `json:"contentDetails"`
}

// videoResponse is the videos endpoint envelope.
type videoResponse struct {
	Items []videoItem `json:"items"`
}

// Search queries YouTube videos. The search endpoint does not return
// durations, so DurationMs is zero; Resolve fills it in later.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]domain.SearchResult, error) {
	q := url.Values{
		"q":          {query},
		"type":       {"video"},
		"part":       {"snippet"},
		"maxResults": {strconv.Itoa(limit)},
		"key":        {c.apiKey},
	}
	var parsed searchResponse
	if err := c.getJSON(ctx, searchEndpoint, q, &parsed); err != nil {
		return nil, err
	}
	results := make([]domain.SearchResult, 0, len(parsed.Items))
	for _, it := range parsed.Items {
		results = append(results, domain.SearchResult{
			ExternalRef: it.ID.VideoID,
			Title:       it.Snippet.Title,
			Artist:      it.Snippet.ChannelTitle,
			CoverURL:    it.Snippet.Thumbnails.High.URL,
		})
	}
	return results, nil
}

// Resolve fetches full video metadata, converting the ISO 8601 duration to
// milliseconds.
func (c *Client) Resolve(ctx context.Context, externalRef string) (domain.ResolvedMedia, error) {
	q := url.Values{
		"id":   {externalRef},
		"part": {"snippet,contentDetails"},
		"key":  {c.apiKey},
	}
	var parsed videoResponse
	if err := c.getJSON(ctx, videosEndpoint, q, &parsed); err != nil {
		return domain.ResolvedMedia{}, err
	}
	if len(parsed.Items) == 0 {
		return domain.ResolvedMedia{}, ports.NotFound("youtube video", externalRef)
	}
	v := parsed.Items[0]
	durationMs, err := domain.ParseISODuration(v.ContentDetails.Duration)
	if err != nil {
		return domain.ResolvedMedia{}, fmt.Errorf("youtube: video %s: %w", v.ID, err)
	}
	return domain.ResolvedMedia{
		ExternalRef: v.ID,
		Title:       v.Snippet.Title,
		Artist:      v.Snippet.ChannelTitle,
		CoverURL:    v.Snippet.Thumbnails.High.URL,
		PlayableURL: "https://www.youtube.com/watch?v=" + v.ID,
		DurationMs:  durationMs,
	}, nil
}

// getJSON performs a GET on endpoint with q and decodes the JSON body into out.
func (c *Client) getJSON(ctx context.Context, endpoint string, q url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("youtube: request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("youtube: request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("youtube: response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("youtube: status %d: %s", resp.StatusCode, snippet(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("youtube: decode: %w", err)
	}
	return nil
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
