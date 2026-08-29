// Package domain contains the Room Service's domain entities and business
// rules. Room is the aggregate root; Member is modified through Room methods.
package domain

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"time"

	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

// RoomVisibility is the room's discoverability level.
type RoomVisibility int8

// VisibilityUnspecified is the zero value for RoomVisibility. The other
// constants in this block enumerate the room discoverability levels; see
// RoomVisibility for semantics.
const (
	VisibilityUnspecified RoomVisibility = 0
	VisibilityPublic      RoomVisibility = 1
	VisibilityUnlisted    RoomVisibility = 2
	VisibilityPrivate     RoomVisibility = 3
)

// Room is the aggregate root for the room lifecycle + membership.
type Room struct {
	ID          string
	Slug        string
	Name        string
	Description string
	Visibility  RoomVisibility
	OwnerID     string
	MaxMembers  int
	MemberCount int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Member is a user's membership in a room.
type Member struct {
	RoomID   string
	UserID   string
	Role     commonv1.RoomRole
	JoinedAt time.Time
}

// Errors returned by domain validation.
var (
	ErrRoomNameEmpty    = errors.New("room: name must not be empty")
	ErrRoomNameTooLong  = errors.New("room: name exceeds 100 characters")
	ErrRoomFull         = errors.New("room: is full")
	ErrAlreadyMember    = errors.New("room: user is already a member")
	ErrNotMember        = errors.New("room: user is not a member")
	ErrCannotKickOwner  = errors.New("room: cannot kick the owner")
	ErrInsufficientRole = errors.New("room: insufficient room role")
	ErrInvalidRole      = errors.New("room: invalid target role")
)

const defaultMaxMembers = 50
const maxRoomName = 100
const maxSlugLen = 64

// NewRoomParams holds the user-supplied fields for creating a room.
type NewRoomParams struct {
	Name        string
	Description string
	Visibility  RoomVisibility
	MaxMembers  *int
}

// NewRoom constructs a Room in a valid state. ID, Slug, and timestamps are set
// by the caller (the use case assigns a ULID ID and generates the slug).
func NewRoom(now time.Time, id, ownerID string, p NewRoomParams) (Room, error) {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return Room{}, ErrRoomNameEmpty
	}
	if len(name) > maxRoomName {
		return Room{}, ErrRoomNameTooLong
	}
	vis := p.Visibility
	if vis == VisibilityUnspecified {
		vis = VisibilityPublic
	}
	maxMembers := defaultMaxMembers
	if p.MaxMembers != nil && *p.MaxMembers > 0 {
		maxMembers = *p.MaxMembers
	}
	return Room{
		ID:          id,
		Slug:        GenerateSlug(name),
		Name:        name,
		Description: strings.TrimSpace(p.Description),
		Visibility:  vis,
		OwnerID:     ownerID,
		MaxMembers:  maxMembers,
		MemberCount: 0,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// IsFull reports whether the room has reached its member capacity.
func (r *Room) IsFull() bool {
	return r.MemberCount >= r.MaxMembers
}

// IsVisible reports whether the room appears in public listings. Public rooms
// are visible; unlisted and private are not.
func (r *Room) IsVisible() bool {
	return r.Visibility == VisibilityPublic
}

// RequiresInvite reports whether joining this room requires an invite code.
func (r *Room) RequiresInvite() bool {
	return r.Visibility == VisibilityPrivate
}

// ApplyUpdate mutates the room's mutable fields from an UpdateRoom request.
// Only non-nil fields are applied.
func (r *Room) ApplyUpdate(now time.Time, name, desc *string, vis *RoomVisibility, maxM *uint32) {
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed != "" {
			r.Name = trimmed
			r.Slug = GenerateSlug(trimmed)
		}
	}
	if desc != nil {
		r.Description = strings.TrimSpace(*desc)
	}
	if vis != nil && *vis != VisibilityUnspecified {
		r.Visibility = *vis
	}
	if maxM != nil && *maxM > 0 {
		r.MaxMembers = int(*maxM)
	}
	r.UpdatedAt = now
}

// slugRe strips non-alphanumeric characters for slug generation.
var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// GenerateSlug produces a URL-safe kebab-case slug from a name, with a random
// suffix for uniqueness. Example: "My Cool Room" → "my-cool-room-a1b2c3".
// The random suffix makes collisions vanishingly unlikely; the DB UNIQUE
// constraint catches the rest.
func GenerateSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > maxSlugLen-8 {
		s = s[:maxSlugLen-9] // leave room for "-" + 6-char suffix
	}
	suffix := randomSlugSuffix(6)
	if s == "" {
		return suffix
	}
	return s + "-" + suffix
}

// randomSlugSuffix returns n lowercase alphanumeric chars.
func randomSlugSuffix(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[b[i]%byte(len(alphabet))]
	}
	return string(b)
}

// GenerateInviteCode returns a random invite code (22 chars, base64url 16 bytes).
// Used for private/unlisted rooms.
func GenerateInviteCode() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// CanKick reports whether a user with the given role can kick a user with
// targetRole. MODERATOR and OWNER can kick MEMBERS and GUESTS; nobody can
// kick the OWNER; a MODERATOR cannot kick another MODERATOR.
func CanKick(byRole, targetRole commonv1.RoomRole) bool {
	if targetRole == commonv1.RoomRole_ROOM_ROLE_OWNER {
		return false
	}
	if byRole == commonv1.RoomRole_ROOM_ROLE_OWNER {
		return true
	}
	if byRole == commonv1.RoomRole_ROOM_ROLE_MODERATOR {
		return targetRole < commonv1.RoomRole_ROOM_ROLE_MODERATOR
	}
	return false
}

// CanPromote reports whether a user with byRole may set a target to newRole.
// Only the OWNER can change roles (matching ActionPromoteUser in the Default
// policy). Additionally, the OWNER role itself cannot be assigned via this
// path (ownership transfer is a separate future flow).
func CanPromote(byRole commonv1.RoomRole, newRole commonv1.RoomRole) bool {
	if byRole != commonv1.RoomRole_ROOM_ROLE_OWNER {
		return false
	}
	if newRole == commonv1.RoomRole_ROOM_ROLE_OWNER || newRole == commonv1.RoomRole_ROOM_ROLE_UNSPECIFIED {
		return false
	}
	return true
}
