// Package id provides ULID-based identifier generation and parsing.
//
// VibeSync uses ULIDs (Universally Unique Lexicographically Sortable
// Identifiers) for all primary keys. Rationale (ADR-0009):
//
//   - 128-bit, collision-free without a central issuer or coordination.
//   - Time-ordered (48-bit ms timestamp prefix), so b-tree indexes stay
//     balanced under append-heavy workloads (rooms, events, messages).
//   - Canonical 26-char Crockford base32 form is URL-safe and sortable as
//     a byte string — convenient for cursor pagination.
//   - 80 bits of entropy per millisecond is far beyond the birthday-bound
//     collision risk for any realistic insert rate.
//
// The package wraps github.com/oklog/ulid/v2 with VibeSync conventions:
//   - a single source of monotonic, locked entropy, so concurrent callers
//     never collide within the same millisecond;
//   - Parse/ MustParse that accept only canonical uppercase form;
//   - String helpers that return the canonical form for storage and logs.
package id

import (
	"crypto/rand"
	"errors"
	"regexp"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Format is the canonical 26-character Crockford base32 ULID string.
const Format = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	// canonicalRe matches exactly 26 chars of the Crockford base32 alphabet.
	// Case-sensitive on uppercase, matching ULID's canonical form. Lowercase
	// and ambiguous letters (I/L/O/U) are rejected to catch corruption.
	canonicalRe = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

	// ErrInvalidFormat is returned by Parse when the input is not a canonical
	// 26-char ULID.
	ErrInvalidFormat = errors.New("id: must be 26 canonical ULID characters")
)

// lockedSource is a concurrency-safe wrapper around ulid.Monotonic that
// serializes Generate calls. ULID monotonicity is only guaranteed within a
// single goroutine otherwise, and our services generate IDs from many
// goroutines; a mutex here is far cheaper than a collision at insert time.
type lockedSource struct {
	mu  sync.Mutex
	msr ulid.MonotonicEntropy
}

var defaultSource = func() *lockedSource {
	// crypto/rand-backed monotonic entropy. The Monotonic reader guarantees
	// strictly increasing values within a millisecond, falling back to the
	// random tail if ms rolls back (clock skew). rand.Reader satisfies the
	// io.Reader interface; ulid copies 80 bits per call.
	return &lockedSource{msr: *ulid.Monotonic(rand.Reader, 0)}
}()

// New returns a fresh canonical ULID string. Safe for concurrent use.
func New() string {
	return NewAt(time.Now())
}

// NewAt returns a ULID for the given wall time. Exposed so tests and the Sync
// Service (which reasons about timestamps explicitly) can pin IDs to a clock.
func NewAt(now time.Time) string {
	defaultSource.mu.Lock()
	defer defaultSource.mu.Unlock()
	id := ulid.MustNew(ulid.Timestamp(now), &defaultSource.msr)
	return id.String()
}

// MustParse parses a canonical ULID string. It panics on malformed input —
// use only for compile-time-known constants or test fixtures.
func MustParse(s string) ulid.ULID {
	if err := validate(s); err != nil {
		panic(err)
	}
	return ulid.MustParse(s)
}

// Parse validates and parses a canonical ULID string. Returns
// ErrInvalidFormat on any deviation from the canonical form.
func Parse(s string) (ulid.ULID, error) {
	if err := validate(s); err != nil {
		return ulid.ULID{}, err
	}
	return ulid.Parse(s)
}

// Valid reports whether s is a canonical ULID string.
func Valid(s string) bool {
	return validate(s) == nil
}

// Time returns the embedded millisecond timestamp as a time.Time. Useful for
// ordering or sharding by age without an index on created_at.
func Time(s string) (time.Time, error) {
	u, err := Parse(s)
	if err != nil {
		return time.Time{}, err
	}
	return ulid.Time(u.Time()), nil
}

func validate(s string) error {
	if !canonicalRe.MatchString(s) {
		return ErrInvalidFormat
	}
	return nil
}
