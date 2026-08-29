package domain

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"
)

// RefreshToken is a single refresh-token row. See ADR-0011 for the rotation
// and reuse-detection model.
//
// Wire format presented to clients: "<selector>.<validator>", both random.
// The selector is stored plaintext (indexed for O(1) lookup at refresh time);
// the validator is stored only as ValidatorHash (argon2). A DB compromise
// therefore yields selectors but no usable tokens — an attacker must still
// brute-force the validator hash per row.
type RefreshToken struct {
	ID            string
	FamilyID      string // the session this token belongs to; family-wide revocation key
	UserID        string
	SessionID     string
	Selector      string // plaintext; indexed UNIQUE for lookup
	ValidatorHash string // argon2 hash of the validator half
	RotatedFrom   string // ID of the previous token in the chain; empty for the root
	Status        RefreshTokenStatus
	ExpiresAt     time.Time
	CreatedAt     time.Time
	UsedAt        time.Time // set when rotated
	RevokedAt     time.Time // set when explicitly revoked or family-compromised
}

// Validator bytes / encoding constants. Selector is shorter because it's only
// a lookup key; validator is longer because it's the secret half.
const (
	selectorBytes  = 12 // 16 base64url chars
	validatorBytes = 32 // 43 base64url chars
)

// ErrExpired is returned when a refresh token's ExpiresAt has passed.
var ErrExpired = errors.New("refresh token: expired")

// ErrRevoked is returned when a refresh token has been revoked.
var ErrRevoked = errors.New("refresh token: revoked")

// ErrReuse is returned when a USED token is presented again. This is the
// compromise signal: the caller MUST revoke the entire family.
var ErrReuse = errors.New("refresh token: reuse detected")

// ErrCompromised is returned when a token in an already-compromised family is
// presented. The family was already burned; this is a follow-on attempt.
var ErrCompromised = errors.New("refresh token: family compromised")

// ValidatorMaterial is the raw validator bytes returned alongside a token, to
// be assembled into the presented "<selector>.<validator>" string once and
// then discarded. The plaintext validator is NEVER persisted.
type ValidatorMaterial struct {
	Selector  string // base64url
	Validator string // base64url
}

// PresentationToken returns the wire form "<selector>.<validator>".
func (v ValidatorMaterial) PresentationToken() string {
	return v.Selector + "." + v.Validator
}

// NewRefreshToken constructs a new active refresh token rooted in familyID.
// It returns the domain record (for storage) plus the validator material (for
// one-time return to the client). The validator plaintext is not stored.
//
// hashFn is the PasswordHasher.Hash port; injecting it keeps the domain pure
// (no argon2 import here). rotatedFrom is the previous token's ID when this
// token is the result of a rotation; empty for the session-root token.
func NewRefreshToken(
	now time.Time,
	id, familyID, userID, sessionID, rotatedFrom string,
	ttl time.Duration,
	hashFn func(string) (string, error),
) (RefreshToken, ValidatorMaterial, error) {
	selector, err := randomURL(selectorBytes)
	if err != nil {
		return RefreshToken{}, ValidatorMaterial{}, err
	}
	validator, err := randomURL(validatorBytes)
	if err != nil {
		return RefreshToken{}, ValidatorMaterial{}, err
	}
	vhash, err := hashFn(validator)
	if err != nil {
		return RefreshToken{}, ValidatorMaterial{}, err
	}
	return RefreshToken{
			ID:            id,
			FamilyID:      familyID,
			UserID:        userID,
			SessionID:     sessionID,
			Selector:      selector,
			ValidatorHash: vhash,
			RotatedFrom:   rotatedFrom,
			Status:        RefreshTokenStatusActive,
			ExpiresAt:     now.Add(ttl),
			CreatedAt:     now,
		}, ValidatorMaterial{Selector: selector, Validator: validator},
		nil
}

// VerifyValidator checks a presented validator against this token's stored
// hash. Returns true on a match. The comparison is constant-time because the
// PasswordHasher.Compare port uses argon2's subkey constant-time comparison.
//
// This method does NOT consult Status or ExpiresAt — those are checked
// separately by ClassifyUse, because the classification drives different
// state-machine transitions and error types.
func (t RefreshToken) VerifyValidator(presentedValidator string, compareFn func(hash, candidate string) bool) bool {
	return compareFn(t.ValidatorHash, presentedValidator)
}

// ClassifyUse inspects the token's current state and returns the appropriate
// outcome of presenting it for refresh. This is the heart of ADR-0011.
//
// IMPORTANT: this is a pure classification; it does NOT mutate state. The
// caller (use case) performs the resulting state transition inside the
// Postgres transaction. The returned action tells the caller what to do.
func (t RefreshToken) ClassifyUse(now time.Time) UseAction {
	// Compromised family: any token in a burned family is dead. This is the
	// branch hit by an attacker replaying after reuse was already detected.
	if t.Status == RefreshTokenStatusCompromised {
		return UseAction{Outcome: UseOutcomeCompromised, Err: ErrCompromised}
	}
	// Explicitly revoked (logout). Idempotent refuse.
	if t.Status == RefreshTokenStatusRevoked {
		return UseAction{Outcome: UseOutcomeRevoked, Err: ErrRevoked}
	}
	// Expiry is checked dynamically (no proactive sweep) so a row can stay
	// "active" in storage but be functionally expired.
	if !now.Before(t.ExpiresAt) {
		return UseAction{Outcome: UseOutcomeExpired, Err: ErrExpired}
	}
	// USED — the security tripwire. A used token being presented again means
	// either a bug (we shouldn't have returned it twice) or an attacker who
	// captured the previous token in the chain. Treat as compromise: the
	// caller MUST revoke the whole family.
	if t.Status == RefreshTokenStatusUsed {
		return UseAction{Outcome: UseOutcomeReuse, Err: ErrReuse}
	}
	// Active and not expired: the happy path.
	if t.Status == RefreshTokenStatusActive {
		return UseAction{Outcome: UseOutcomeRotate}
	}
	// Defensive: any other status (Unspecified, Expired-stored) is invalid.
	return UseAction{Outcome: UseOutcomeRevoked, Err: ErrRevoked}
}

// UseAction is the result of ClassifyUse. The caller switches on Outcome and
// performs the state transition; Err is the typed error to wrap when the
// outcome is not UseOutcomeRotate.
type UseAction struct {
	Outcome UseOutcome
	Err     error // nil when Outcome == UseOutcomeRotate
}

// UseOutcome enumerates the possible outcomes of presenting a refresh token.
type UseOutcome int8

const (
	// UseOutcomeUnspecified is the zero value and is never returned.
	UseOutcomeUnspecified UseOutcome = iota
	// UseOutcomeRotate means the token is active+valid; rotate it (mark used, issue new).
	UseOutcomeRotate
	// UseOutcomeReuse means a USED token was presented again; revoke the family.
	UseOutcomeReuse
	// UseOutcomeCompromised means the family was already burned; refuse.
	UseOutcomeCompromised
	// UseOutcomeRevoked means the token was explicitly revoked; refuse.
	UseOutcomeRevoked
	// UseOutcomeExpired means the token's TTL elapsed; refuse.
	UseOutcomeExpired
)

// MarkUsed transitions the token to the Used state. Called by the use case
// after ClassifyUse returns UseOutcomeRotate and the new token in the chain
// has been staged in the same transaction.
func (t *RefreshToken) MarkUsed(now time.Time) {
	t.Status = RefreshTokenStatusUsed
	t.UsedAt = now
}

// MarkRevoked transitions the token to Revoked. Used by Logout.
func (t *RefreshToken) MarkRevoked(now time.Time) {
	t.Status = RefreshTokenStatusRevoked
	t.RevokedAt = now
}

// MarkCompromised transitions the token to Compromised. Called for every token
// in a family when reuse is detected (the family-wide revocation).
func (t *RefreshToken) MarkCompromised(now time.Time) {
	t.Status = RefreshTokenStatusCompromised
	t.RevokedAt = now
}

// randomURL returns n random bytes base64url-encoded (no padding). Used for
// selectors and validators. crypto/rand is the right source — these are
// bearer secrets, not performance-sensitive tokens.
func randomURL(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
