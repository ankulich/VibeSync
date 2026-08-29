package domain

import (
	"testing"
)

func TestParseISODuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input   string
		wantMs  int64
		wantErr bool
	}{
		{"PT4M13S", 4*60*1000 + 13*1000, false},
		{"PT1H2M3S", 1*3600*1000 + 2*60*1000 + 3*1000, false},
		{"PT30S", 30 * 1000, false},
		{"PT1H", 3600 * 1000, false},
		{"PT2M", 2 * 60 * 1000, false},
		{"P1DT2H3M4S", 86400*1000 + 2*3600*1000 + 3*60*1000 + 4*1000, false},
		{"PT0S", 0, false},
		// Error cases
		{"", 0, true},
		{"4M13S", 0, true},   // missing P/T prefix
		{"PT", 0, true},      // empty time part
		{"PX13S", 0, true},   // invalid unit
		{"PT4X13S", 0, true}, // invalid unit in middle
		{"PT4M13", 0, true},  // trailing number without unit
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got, err := ParseISODuration(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("ParseISODuration(%q) = %d, want error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseISODuration(%q) error: %v", tc.input, err)
			}
			if got != tc.wantMs {
				t.Errorf("ParseISODuration(%q) = %d ms, want %d ms", tc.input, got, tc.wantMs)
			}
		})
	}
}

func TestSearchResultFields(t *testing.T) {
	t.Parallel()
	sr := SearchResult{
		ExternalRef: "spotify:track:abc",
		Title:       "Song",
		Artist:      "Artist",
		CoverURL:    "https://example.com/cover.jpg",
		DurationMs:  210000,
	}
	if sr.ExternalRef == "" || sr.Title == "" {
		t.Error("fields should be set")
	}
}

func TestResolvedMediaFields(t *testing.T) {
	t.Parallel()
	rm := ResolvedMedia{
		ExternalRef: "dQw4w9WgXcQ",
		Title:       "Video",
		Artist:      "Channel",
		PlayableURL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		DurationMs:  213000,
	}
	if rm.PlayableURL == "" {
		t.Error("playable URL should be set")
	}
}
