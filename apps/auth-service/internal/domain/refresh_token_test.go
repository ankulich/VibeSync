package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// stubHasher is a minimal port-shaped hasher for domain tests. It uses a
// reversible XOR-based transform (insecure!) so tests don't pay argon2's
// cost; the production hasher is exercised by the crypto package's own tests.
// The transform is non-trivial enough that the hash does NOT contain the
// plaintext as a substring (a property the domain test asserts).
type stubHasher struct{}

const stubXor = 0x5A

func (stubHasher) Hash(plaintext string) (string, error) {
	b := []byte(plaintext)
	for i := range b {
		b[i] ^= stubXor
	}
	return "h:" + string(b), nil
}
func (stubHasher) Compare(hash, plaintext string) bool {
	got, _ := stubHasher{}.Hash(plaintext)
	return hash == got
}

// newTestToken builds a fresh active token with deterministic fields for tests.
func newTestToken(t *testing.T, status RefreshTokenStatus) RefreshToken {
	t.Helper()
	hasher := stubHasher{}
	rt, _, err := NewRefreshToken(
		time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
		"rt_1", "fam_1", "u_1", "s_1", "",
		time.Hour, hasher.Hash,
	)
	if err != nil {
		t.Fatalf("NewRefreshToken: %v", err)
	}
	rt.Status = status
	return rt
}

func TestNewRefreshTokenProducesSelectorDotValidator(t *testing.T) {
	t.Parallel()
	hasher := stubHasher{}
	rt, mat, err := NewRefreshToken(time.Now(), "rt_1", "fam", "u", "s", "", time.Hour, hasher.Hash)
	if err != nil {
		t.Fatal(err)
	}
	tok := mat.PresentationToken()
	if !strings.Contains(tok, ".") {
		t.Fatalf("presentation token %q must contain '.'", tok)
	}
	// Selector on the token must match the stored record's Selector.
	if !strings.HasPrefix(tok, rt.Selector+".") {
		t.Errorf("token must start with selector; got %q, want prefix %q.", tok, rt.Selector)
	}
	// Stored record must NOT contain the validator plaintext anywhere.
	if strings.Contains(rt.ValidatorHash, mat.Validator) {
		t.Error("ValidatorHash must not contain the plaintext validator")
	}
	if rt.Status != RefreshTokenStatusActive {
		t.Errorf("new token must be Active; got %v", rt.Status)
	}
}

func TestClassifyUseHappyPath(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	rt := newTestToken(t, RefreshTokenStatusActive)
	action := rt.ClassifyUse(now)
	if action.Outcome != UseOutcomeRotate {
		t.Errorf("active token must classify as Rotate; got %v", action.Outcome)
	}
	if action.Err != nil {
		t.Errorf("Rotate must have nil err; got %v", action.Err)
	}
}

func TestClassifyUseExpired(t *testing.T) {
	t.Parallel()
	// Token that expired 10 minutes ago.
	rt := newTestToken(t, RefreshTokenStatusActive)
	now := rt.ExpiresAt.Add(10 * time.Minute)
	action := rt.ClassifyUse(now)
	if action.Outcome != UseOutcomeExpired {
		t.Errorf("expired token must classify as Expired; got %v", action.Outcome)
	}
	if !errors.Is(action.Err, ErrExpired) {
		t.Errorf("Err must be ErrExpired; got %v", action.Err)
	}
}

func TestClassifyUseReuseDetected(t *testing.T) {
	t.Parallel()
	rt := newTestToken(t, RefreshTokenStatusUsed)
	now := rt.ExpiresAt.Add(-time.Minute) // not yet expired by time
	action := rt.ClassifyUse(now)
	if action.Outcome != UseOutcomeReuse {
		t.Fatalf("USED token must classify as Reuse; got %v", action.Outcome)
	}
	if !errors.Is(action.Err, ErrReuse) {
		t.Errorf("Err must be ErrReuse; got %v", action.Err)
	}
}

func TestClassifyUseCompromisedFamily(t *testing.T) {
	t.Parallel()
	rt := newTestToken(t, RefreshTokenStatusCompromised)
	action := rt.ClassifyUse(time.Now())
	if action.Outcome != UseOutcomeCompromised {
		t.Fatalf("compromised token must classify as Compromised; got %v", action.Outcome)
	}
	if !errors.Is(action.Err, ErrCompromised) {
		t.Errorf("Err must be ErrCompromised; got %v", action.Err)
	}
}

func TestClassifyUseRevoked(t *testing.T) {
	t.Parallel()
	rt := newTestToken(t, RefreshTokenStatusRevoked)
	action := rt.ClassifyUse(time.Now())
	if action.Outcome != UseOutcomeRevoked {
		t.Fatalf("revoked token must classify as Revoked; got %v", action.Outcome)
	}
	if !errors.Is(action.Err, ErrRevoked) {
		t.Errorf("Err must be ErrRevoked; got %v", action.Err)
	}
}

func TestMarkUsedTransitions(t *testing.T) {
	t.Parallel()
	rt := newTestToken(t, RefreshTokenStatusActive)
	now := time.Now()
	rt.MarkUsed(now)
	if rt.Status != RefreshTokenStatusUsed {
		t.Errorf("after MarkUsed, status must be Used; got %v", rt.Status)
	}
	if !rt.UsedAt.Equal(now) {
		t.Errorf("UsedAt must be set; got %v", rt.UsedAt)
	}
}

func TestMarkCompromisedTransitions(t *testing.T) {
	t.Parallel()
	rt := newTestToken(t, RefreshTokenStatusActive)
	now := time.Now()
	rt.MarkCompromised(now)
	if rt.Status != RefreshTokenStatusCompromised {
		t.Errorf("after MarkCompromised, status must be Compromised; got %v", rt.Status)
	}
}

func TestVerifyValidatorRejectsWrongHalf(t *testing.T) {
	t.Parallel()
	hasher := stubHasher{}
	rt, mat, err := NewRefreshToken(time.Now(), "rt", "fam", "u", "s", "", time.Hour, hasher.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if !rt.VerifyValidator(mat.Validator, hasher.Compare) {
		t.Error("correct validator must verify")
	}
	if rt.VerifyValidator("wrong-validator", hasher.Compare) {
		t.Error("wrong validator must NOT verify")
	}
}
