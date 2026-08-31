package youtube

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vibesync/apps/provider-service/internal/ports"
)

// newTestClient builds a Client pointed at a test server.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return newClient(srv.Client(), srv.URL)
}

func TestSearchUnsupported(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("Search must not perform any HTTP calls")
	})
	_, err := c.Search(context.Background(), "query", 10)
	if !errors.Is(err, ports.ErrSearchUnsupported) {
		t.Errorf("Search error = %v, want ports.ErrSearchUnsupported", err)
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	var gotPath, gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"title": "Rick Astley - Never Gonna Give You Up",
			"author_name": "Rick Astley",
			"thumbnail_url": "https://i.ytimg.com/vi/dQw4w9WgXcQ/hqdefault.jpg"
		}`))
	})
	media, err := c.Resolve(context.Background(), "dQw4w9WgXcQ")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if gotPath != "/oembed" {
		t.Errorf("request path = %q, want /oembed", gotPath)
	}
	if !strings.Contains(gotQuery, "format=json") {
		t.Errorf("query %q missing format=json", gotQuery)
	}
	if !strings.Contains(gotQuery, "v%3DdQw4w9WgXcQ") {
		t.Errorf("query %q missing encoded watch url", gotQuery)
	}
	if media.Title != "Rick Astley - Never Gonna Give You Up" {
		t.Errorf("Title = %q", media.Title)
	}
	if media.Artist != "Rick Astley" {
		t.Errorf("Artist = %q", media.Artist)
	}
	if media.CoverURL != "https://i.ytimg.com/vi/dQw4w9WgXcQ/hqdefault.jpg" {
		t.Errorf("CoverURL = %q", media.CoverURL)
	}
	if media.PlayableURL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Errorf("PlayableURL = %q", media.PlayableURL)
	}
	if media.DurationMs != 0 {
		t.Errorf("DurationMs = %d, want 0 (player reports it)", media.DurationMs)
	}
}

func TestResolveNotEmbeddable(t *testing.T) {
	t.Parallel()
	// oEmbed answers 400/401/403/404 for missing, private or embed-disabled
	// videos; all map to the canonical not-found error.
	for _, status := range []int{400, 401, 403, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
			_, err := c.Resolve(context.Background(), "abc12345678")
			if !errors.Is(err, ports.ErrNotFound) {
				t.Errorf("error = %v, want ports.ErrNotFound", err)
			}
		})
	}
}

func TestResolveServerError(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	})
	_, err := c.Resolve(context.Background(), "abc12345678")
	if err == nil {
		t.Fatal("want error on 500, got nil")
	}
	if errors.Is(err, ports.ErrNotFound) {
		t.Errorf("500 must not map to not-found, got %v", err)
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("error %q missing status", err.Error())
	}
}

func TestResolveBadJSON(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})
	_, err := c.Resolve(context.Background(), "abc12345678")
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("error = %v, want decode failure", err)
	}
}
