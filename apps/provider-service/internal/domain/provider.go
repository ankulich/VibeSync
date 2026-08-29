// Package domain holds the Provider Service core types.
package domain

import (
	"fmt"
	"strings"
)

// SearchResult is a single hit from an external provider search.
type SearchResult struct {
	ExternalRef string
	Title       string
	Artist      string
	CoverURL    string
	DurationMs  int64
}

// ResolvedMedia is fully resolved external media, ready for the Media Service
// to enqueue and for the Playback Service to stream.
type ResolvedMedia struct {
	ExternalRef string
	Title       string
	Artist      string
	CoverURL    string
	PlayableURL string
	DurationMs  int64
}

// ParseISODuration parses an ISO 8601 duration such as "PT4M13S", "PT1H2M3S",
// or "P1DT2H" into milliseconds. The D (date) and H/M/S (time) components are
// supported, which covers everything the YouTube Data API emits in
// contentDetails.duration.
func ParseISODuration(s string) (int64, error) {
	rest := strings.TrimPrefix(s, "P")
	if len(rest) == len(s) || rest == "" {
		return 0, fmt.Errorf("domain: invalid ISO 8601 duration %q: missing P prefix", s)
	}
	var (
		total   int64
		num     int64
		digits  bool
		inTime  bool
		sawUnit bool
	)
	for _, r := range rest {
		switch {
		case r == 'T':
			if inTime {
				return 0, fmt.Errorf("domain: invalid ISO 8601 duration %q: duplicate T", s)
			}
			inTime = true
		case r >= '0' && r <= '9':
			num = num*10 + int64(r-'0')
			digits = true
		default:
			ms, ok := unitMillis(r, inTime)
			if !ok {
				return 0, fmt.Errorf("domain: invalid ISO 8601 duration %q: unsupported unit %q", s, r)
			}
			if !digits {
				return 0, fmt.Errorf("domain: invalid ISO 8601 duration %q: missing number before %q", s, r)
			}
			total += num * ms
			num, digits, sawUnit = 0, false, true
		}
	}
	if digits {
		return 0, fmt.Errorf("domain: invalid ISO 8601 duration %q: trailing number without unit", s)
	}
	if !sawUnit {
		return 0, fmt.Errorf("domain: invalid ISO 8601 duration %q: no components", s)
	}
	return total, nil
}

// unitMillis maps a duration unit rune to its millisecond weight. Date units
// are only valid before the T separator and time units only after it.
func unitMillis(r rune, inTime bool) (int64, bool) {
	switch r {
	case 'D':
		if inTime {
			return 0, false
		}
		return 24 * 3_600_000, true
	case 'H':
		if !inTime {
			return 0, false
		}
		return 3_600_000, true
	case 'M':
		if !inTime {
			// Months in the date part are not a fixed duration; reject them.
			return 0, false
		}
		return 60_000, true
	case 'S':
		if !inTime {
			return 0, false
		}
		return 1_000, true
	default:
		return 0, false
	}
}
