package id

import (
	"strings"
	"testing"
	"time"
)

func TestNewProducesCanonical(t *testing.T) {
	t.Parallel()
	s := New()
	if !Valid(s) {
		t.Errorf("New() produced invalid id %q", s)
	}
	if len(s) != 26 {
		t.Errorf("New() length = %d, want 26", len(s))
	}
}

func TestNewIsMonotonicWithinMillisecond(t *testing.T) {
	t.Parallel()
	// Pinning both to the same ms forces the monotonic path.
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	const n = 1000
	ids := make(map[string]struct{}, n)
	for range n {
		s := NewAt(now)
		if _, dup := ids[s]; dup {
			t.Fatalf("duplicate id within same ms: %s", s)
		}
		ids[s] = struct{}{}
	}
}

func TestNewAtTimeOrderingHoldsAcrossMillis(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	earlier := NewAt(base)
	later := NewAt(base.Add(2 * time.Millisecond))
	if earlier >= later {
		t.Errorf("expected string ordering to track time: %q >= %q", earlier, later)
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",                            // empty
		"01BX5ZZKBKACTAV9WEVGEMM",     // 25 chars
		"i" + strings.Repeat("0", 25), // 26 chars but starts with lowercase
		strings.ToLower("01BX5ZZKBKACTAV9WEVGEMMVR") + "X", // lowercase
		"01BX5ZZKBKACTAV9WEVGEMMVR" + "!",                  // punctuation
		"0000000000000000000000000I",                       // ambiguous I
		"0000000000000000000000000O",                       // ambiguous O
		"0000000000000000000000000U",                       // ambiguous U
	}
	for _, in := range cases {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) succeeded; want ErrInvalidFormat", in)
		}
	}
}

func TestParseAcceptsCanonical(t *testing.T) {
	t.Parallel()
	// Round-trip a freshly generated ULID: it is canonical by construction.
	good := New()
	u, err := Parse(good)
	if err != nil {
		t.Fatalf("Parse(%q) failed: %v", good, err)
	}
	if u.String() != good {
		t.Errorf("round-trip mismatch: %q != %q", u.String(), good)
	}
}

func TestTimeExtractsTimestamp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	s := NewAt(now)
	got, err := Time(s)
	if err != nil {
		t.Fatalf("Time(%q) failed: %v", s, err)
	}
	// ULID timestamps are millisecond-resolution; compare truncated.
	if got.Truncate(time.Millisecond).Equal(now.Truncate(time.Millisecond)) {
		return
	}
	t.Errorf("Time() = %v, want %v", got, now)
}

func TestMustParsePanicsOnBadInput(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustParse must panic on invalid input")
		}
	}()
	_ = MustParse("not-a-ulid")
}
