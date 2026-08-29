package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewMediaValidates(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	cases := []struct {
		name    string
		params  NewMediaParams
		wantErr error
	}{
		{"happy path audio", NewMediaParams{Title: "Track One", Kind: KindAudio}, nil},
		{"happy path video", NewMediaParams{Title: "Video", Kind: KindVideo}, nil},
		{"empty title", NewMediaParams{Title: ""}, ErrMediaTitleEmpty},
		{"whitespace title", NewMediaParams{Title: "   "}, ErrMediaTitleEmpty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, err := NewMedia(now, "01J6MEDIA000000000000000A", tc.params)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m.Title == "" {
				t.Error("title should not be empty")
			}
			if m.ID == "" {
				t.Error("id should be set")
			}
			if !m.CreatedAt.Equal(now) {
				t.Error("CreatedAt should be now")
			}
		})
	}
}

func TestNewMediaDefaultsKindToAudio(t *testing.T) {
	t.Parallel()
	m, err := NewMedia(time.Now(), "id", NewMediaParams{Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Kind != KindAudio {
		t.Errorf("kind should default to audio; got %v", m.Kind)
	}
}

func TestNewMediaDefaultsSourceToProvider(t *testing.T) {
	t.Parallel()
	m, _ := NewMedia(time.Now(), "id", NewMediaParams{Title: "T"})
	if m.Source != SourceProvider {
		t.Errorf("source should default to provider; got %v", m.Source)
	}
}

func TestNewMediaTrimsFields(t *testing.T) {
	t.Parallel()
	m, _ := NewMedia(time.Now(), "id", NewMediaParams{
		Title: "  Song  ", Artist: "  Artist  ", ExternalRef: "  ref  ",
	})
	if m.Title != "Song" {
		t.Errorf("title should be trimmed; got %q", m.Title)
	}
	if m.Artist != "Artist" {
		t.Errorf("artist should be trimmed; got %q", m.Artist)
	}
	if m.ExternalRef != "ref" {
		t.Errorf("external_ref should be trimmed; got %q", m.ExternalRef)
	}
}

func TestNewMediaLongTitleAccepted(t *testing.T) {
	t.Parallel()
	// No length limit on title (VARCHAR(255) enforced at the DB level).
	m, err := NewMedia(time.Now(), "id", NewMediaParams{Title: strings.Repeat("x", 300)})
	if err != nil {
		t.Fatalf("long title should not error in domain; got %v", err)
	}
	if m.Title != strings.Repeat("x", 300) {
		t.Error("title should be preserved")
	}
}

func TestMediaKindEnumValues(t *testing.T) {
	t.Parallel()
	if KindAudio != 1 || KindVideo != 2 {
		t.Errorf("kind enum values must match proto: audio=1, video=2; got %d, %d", KindAudio, KindVideo)
	}
}

func TestMediaSourceEnumValues(t *testing.T) {
	t.Parallel()
	if SourceProvider != 1 || SourceUpload != 2 || SourceCache != 3 {
		t.Errorf("source enum values must match proto: provider=1, upload=2, cache=3; got %d, %d, %d",
			SourceProvider, SourceUpload, SourceCache)
	}
}
