package domain

import (
	"errors"
	"regexp"
	"strings"
	"time"

	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

// User is the Auth-owned user aggregate. See ADR-0010: Auth owns the canonical
// user record; the User Service (Phase 5) builds its read model from the
// user.created.v1 event.
//
// A user created via password login has a non-empty PasswordHash; a user
// created via OAuth has an empty PasswordHash (login is only via OAuth).
type User struct {
	ID           string
	Email        string
	Username     string
	DisplayName  string
	AvatarURL    string
	PasswordHash string // empty when the user authenticates only via OAuth
	SystemRole   commonv1.SystemRole
	Status       UserStatus
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Errors returned by user validation. Typed so callers can branch.
var (
	// ErrEmailInvalid is returned when an email fails format validation.
	ErrEmailInvalid = errors.New("user: invalid email")
	// ErrUsernameInvalid is returned when a username fails format validation.
	ErrUsernameInvalid = errors.New("user: invalid username")
	// ErrDisplayNameTooLong is returned when a display name exceeds 100 chars.
	ErrDisplayNameTooLong = errors.New("user: display name too long")
)

var (
	// emailRe is intentionally pragmatic: it accepts the common shapes and
	// rejects obvious garbage. Strict RFC 5322 validation is hostile to users
	// and provides little security value; deliverability is verified by the
	// OAuth provider or a future email-verification flow.
	emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	// usernameRe allows 3..32 chars of [a-z0-9_], starting with a letter.
	usernameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{2,31}$`)
)

// NewUserParams holds the inputs for constructing a new User. ID, role,
// timestamps, and status are set by the constructor; callers supply only the
// user-controlled fields.
type NewUserParams struct {
	Email       string
	Username    string
	DisplayName string
	AvatarURL   string
}

// NewUser constructs a User in the Active state with the default role (USER).
// It validates user-supplied fields and returns a typed error on failure.
// ID, timestamps, and password hash are zero/empty; the repository assigns ID
// and timestamps at insert (the use case may override ID via idgen).
func NewUser(now time.Time, p NewUserParams) (User, error) {
	email := strings.TrimSpace(strings.ToLower(p.Email))
	if !emailRe.MatchString(email) {
		return User{}, ErrEmailInvalid
	}
	username := strings.TrimSpace(strings.ToLower(p.Username))
	if !usernameRe.MatchString(username) {
		return User{}, ErrUsernameInvalid
	}
	display := strings.TrimSpace(p.DisplayName)
	if len(display) > 100 {
		return User{}, ErrDisplayNameTooLong
	}
	if display == "" {
		// Default the display name to the username; UIs may let users edit it.
		display = username
	}
	return User{
		Email:       email,
		Username:    username,
		DisplayName: display,
		AvatarURL:   strings.TrimSpace(p.AvatarURL),
		SystemRole:  commonv1.SystemRole_SYSTEM_ROLE_USER,
		Status:      UserStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// CanAuthenticate reports whether the user may log in: active and (has a
// password OR is OAuth-only). The "OAuth-only" determination is caller-driven
// (the use case knows whether a provider link exists); this method only checks
// the status.
func (u User) CanAuthenticate() bool {
	return u.Status.CanLogin()
}

// SetPassword records the password hash. The hash is computed by the
// PasswordHasher port (argon2id); the domain only carries the result.
func (u *User) SetPassword(hash string) {
	u.PasswordHash = hash
	u.UpdatedAt = time.Now().UTC()
}

// HasPassword reports whether the user has a password (vs. OAuth-only).
func (u User) HasPassword() bool { return u.PasswordHash != "" }
