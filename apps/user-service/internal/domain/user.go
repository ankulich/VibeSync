// Package domain contains the User Service's domain entities. This is a
// read-model projection of users — the Auth Service (Phase 4) owns the
// canonical `auth.users` table; the User Service consumes `user.created.v1`
// events and maintains a read-optimized copy for profile lookups and listings.
//
// See ADR-0010 (Auth owns user records) and ADR-0014 (User Service owns
// profile updates).
package domain

import (
	"errors"
	"strings"
	"time"

	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

// User is the read-model projection of a VibeSync user. The shape mirrors the
// User proto (gen/go/vibesync/user/v1) but is a plain Go struct so the domain
// stays free of generated-code dependencies beyond the role enum.
type User struct {
	ID          string
	Email       string
	Username    string
	DisplayName string
	AvatarURL   string
	SystemRole  commonv1.SystemRole
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// UserCreatedV1 is the deserialized payload of a `user.created.v1` Kafka event.
// The Auth Service emits this via its outbox on first OAuth login (see
// apps/auth-service/internal/app/oauth.go). Field names match the JSON keys
// Auth's completeOAuthUpsert marshals.
type UserCreatedV1 struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	Username  string `json:"username"`
	Provider  string `json:"provider"`
	CreatedAt string `json:"created_at"` // RFC3339 string
}

// Errors returned by projection and validation.
var (
	// ErrInvalidEvent is returned when a user.created.v1 event is missing
	// required fields (user_id, email, username).
	ErrInvalidEvent = errors.New("user: invalid user.created.v1 event")
)

// ProjectFromEvent converts a UserCreatedV1 event payload into a domain User
// ready for Upsert. Fields absent from the event (display_name, avatar_url,
// system_role) are backfilled with sensible defaults:
//   - display_name defaults to username
//   - avatar_url defaults to empty
//   - system_role defaults to SYSTEM_ROLE_USER
//   - created_at is parsed from the RFC3339 string
func ProjectFromEvent(e UserCreatedV1, now time.Time) (User, error) {
	if e.UserID == "" || e.Email == "" || e.Username == "" {
		return User{}, ErrInvalidEvent
	}
	created := parseRFC3339(e.CreatedAt, now)
	username := strings.ToLower(strings.TrimSpace(e.Username))
	return User{
		ID:          e.UserID,
		Email:       strings.ToLower(strings.TrimSpace(e.Email)),
		Username:    username,
		DisplayName: username,
		AvatarURL:   "",
		SystemRole:  commonv1.SystemRole_SYSTEM_ROLE_USER,
		CreatedAt:   created,
		UpdatedAt:   created,
	}, nil
}

// parseRFC3339 parses the event's created_at string. If the string is empty or
// unparseable, the caller's `now` is used as a fallback — better to project the
// row with an approximate timestamp than to drop the event. Never returns an
// error: a bad timestamp is always recovered via the fallback.
func parseRFC3339(s string, fallback time.Time) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return fallback
	}
	return t
}

// ApplyUpdate mutates a User with the optional display_name and avatar_url
// from an UpdateUser request. Only non-nil fields are applied; the UpdatedAt
// timestamp is refreshed. Returns the updated user (the receiver is mutated).
func (u *User) ApplyUpdate(now time.Time, displayName, avatarURL *string) {
	if displayName != nil {
		trimmed := strings.TrimSpace(*displayName)
		if trimmed != "" {
			u.DisplayName = trimmed
		}
	}
	if avatarURL != nil {
		u.AvatarURL = strings.TrimSpace(*avatarURL)
	}
	u.UpdatedAt = now
}
