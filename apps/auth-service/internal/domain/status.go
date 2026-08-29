package domain

// Status enums for Auth domain entities. Persisted as SMALLINT in Postgres;
// the numeric values are part of the storage contract — do NOT renumber.

// UserStatus is the lifecycle state of a user account.
type UserStatus int8

const (
	// UserStatusUnspecified is the zero value and is invalid for persisted rows.
	UserStatusUnspecified UserStatus = iota
	// UserStatusActive is a normal, login-capable account.
	UserStatusActive
	// UserStatusSuspended is administratively disabled; login refuses.
	UserStatusSuspended
	// UserStatusDeleted is a soft-deleted account (PII retained for audit only
	// by future retention policy; login refuses).
	UserStatusDeleted
)

// CanLogin reports whether a user in this status may authenticate.
func (s UserStatus) CanLogin() bool {
	return s == UserStatusActive
}

// RefreshTokenStatus is the lifecycle state of a single refresh token.
//
// The state machine is the security-critical part of ADR-0011:
//
//	active  --rotate-->  used      (normal refresh; one transition only)
//	used    --reuse--->  compromised (reuse detected → family revoked)
//	active  --logout--> revoked    (explicit logout)
//	any     --expire--> expired    (time-based; checked at use)
type RefreshTokenStatus int8

const (
	// RefreshTokenStatusUnspecified is invalid.
	RefreshTokenStatusUnspecified RefreshTokenStatus = iota
	// RefreshTokenStatusActive is a usable, un-rotated token.
	RefreshTokenStatusActive
	// RefreshTokenStatusUsed is a token that was rotated; presenting it again
	// is reuse and revokes the family.
	RefreshTokenStatusUsed
	// RefreshTokenStatusRevoked is an explicitly-logged-out token.
	RefreshTokenStatusRevoked
	// RefreshTokenStatusCompromised marks any token in a family where reuse
	// was detected. The entire family transitions to this state atomically.
	RefreshTokenStatusCompromised
	// RefreshTokenStatusExpired is set lazily at use-time when now > ExpiresAt.
	// Stored rows do not proactively flip to Expired; the check is dynamic.
	RefreshTokenStatusExpired
)

// SigningKeyStatus is the lifecycle state of a JWT signing key.
//
//	active  --rotate-->  retired
//
// Exactly one key is active at a time. Retired keys verify until removed
// (configurable retention); only the active key signs.
type SigningKeyStatus int8

const (
	// SigningKeyStatusUnspecified is invalid.
	SigningKeyStatusUnspecified SigningKeyStatus = iota
	// SigningKeyStatusActive is the current signing key. Exactly one.
	SigningKeyStatusActive
	// SigningKeyStatusRetired no longer signs but still verifies outstanding
	// tokens issued before rotation.
	SigningKeyStatusRetired
)
