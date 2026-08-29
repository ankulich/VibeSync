package domain

import (
	"strings"
	"testing"
	"time"

	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

func TestNewRoomValidates(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	cases := []struct {
		name    string
		params  NewRoomParams
		wantErr error
	}{
		{"happy path", NewRoomParams{Name: "My Cool Room"}, nil},
		{"empty name", NewRoomParams{Name: ""}, ErrRoomNameEmpty},
		{"whitespace name", NewRoomParams{Name: "   "}, ErrRoomNameEmpty},
		{"name too long", NewRoomParams{Name: strings.Repeat("x", 101)}, ErrRoomNameTooLong},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := NewRoom(now, "01J6ROOM000000000000000A", "owner_1", tc.params)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("expected %v, got nil", tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r.Name == "" {
				t.Error("name should not be empty")
			}
			if r.OwnerID != "owner_1" {
				t.Errorf("owner_id = %q", r.OwnerID)
			}
			if r.MemberCount != 0 {
				t.Errorf("new room should have 0 members; got %d", r.MemberCount)
			}
			if r.MaxMembers != defaultMaxMembers {
				t.Errorf("default max_members should be %d; got %d", defaultMaxMembers, r.MaxMembers)
			}
		})
	}
}

func TestNewRoomDefaultsVisibilityToPublic(t *testing.T) {
	t.Parallel()
	r, _ := NewRoom(time.Now(), "id", "owner", NewRoomParams{Name: "Test"})
	if r.Visibility != VisibilityPublic {
		t.Errorf("visibility should default to Public; got %v", r.Visibility)
	}
}

func TestNewRoomCustomMaxMembers(t *testing.T) {
	t.Parallel()
	custom := 10
	r, _ := NewRoom(time.Now(), "id", "owner", NewRoomParams{Name: "Test", MaxMembers: &custom})
	if r.MaxMembers != 10 {
		t.Errorf("max_members = %d, want 10", r.MaxMembers)
	}
}

func TestGenerateSlugProducesKebabCaseWithSuffix(t *testing.T) {
	t.Parallel()
	slug := GenerateSlug("My Cool Room!")
	if !strings.HasPrefix(slug, "my-cool-room-") {
		t.Errorf("slug %q should start with 'my-cool-room-'", slug)
	}
	if len(slug) <= len("my-cool-room-") {
		t.Errorf("slug should have a random suffix; got %q", slug)
	}
}

func TestGenerateSlugIsLowercase(t *testing.T) {
	t.Parallel()
	slug := GenerateSlug("UPPERCASE NAME")
	if slug != strings.ToLower(slug) {
		t.Errorf("slug should be lowercase; got %q", slug)
	}
}

func TestGenerateSlugStripsSpecialChars(t *testing.T) {
	t.Parallel()
	slug := GenerateSlug("Room @#$% Name!!!")
	if strings.ContainsAny(slug, "@#$%!") {
		t.Errorf("slug should not contain special chars; got %q", slug)
	}
}

func TestGenerateSlugUniqueAcrossCalls(t *testing.T) {
	t.Parallel()
	s1 := GenerateSlug("Same Name")
	s2 := GenerateSlug("Same Name")
	if s1 == s2 {
		t.Error("slugs for the same name should differ (random suffix)")
	}
}

func TestIsFullAndRequiresInvite(t *testing.T) {
	t.Parallel()
	r := Room{MaxMembers: 2, MemberCount: 2, Visibility: VisibilityPrivate}
	if !r.IsFull() {
		t.Error("room with member_count == max_members should be full")
	}
	if !r.RequiresInvite() {
		t.Error("private room should require invite")
	}
	r2 := Room{MaxMembers: 50, MemberCount: 10, Visibility: VisibilityPublic}
	if r2.IsFull() {
		t.Error("room with member_count < max_members should not be full")
	}
	if r2.RequiresInvite() {
		t.Error("public room should not require invite")
	}
}

func TestApplyUpdateName(t *testing.T) {
	t.Parallel()
	r := Room{ID: "r1", Name: "Old", Slug: "old-slug", Visibility: VisibilityPublic}
	newName := "New Name"
	r.ApplyUpdate(time.Now(), &newName, nil, nil, nil)
	if r.Name != "New Name" {
		t.Errorf("name = %q", r.Name)
	}
	if !strings.HasPrefix(r.Slug, "new-name-") {
		t.Errorf("slug should be regenerated from new name; got %q", r.Slug)
	}
}

func TestApplyUpdateIgnoresEmptyName(t *testing.T) {
	t.Parallel()
	r := Room{ID: "r1", Name: "Keep", Slug: "keep-slug"}
	empty := "  "
	r.ApplyUpdate(time.Now(), &empty, nil, nil, nil)
	if r.Name != "Keep" {
		t.Errorf("empty name should be ignored; got %q", r.Name)
	}
}

func TestCanKickMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		byRole     commonv1.RoomRole
		targetRole commonv1.RoomRole
		want       bool
	}{
		{"owner kicks member", commonv1.RoomRole_ROOM_ROLE_OWNER, commonv1.RoomRole_ROOM_ROLE_MEMBER, true},
		{"owner kicks moderator", commonv1.RoomRole_ROOM_ROLE_OWNER, commonv1.RoomRole_ROOM_ROLE_MODERATOR, true},
		{"moderator kicks member", commonv1.RoomRole_ROOM_ROLE_MODERATOR, commonv1.RoomRole_ROOM_ROLE_MEMBER, true},
		{"moderator kicks guest", commonv1.RoomRole_ROOM_ROLE_MODERATOR, commonv1.RoomRole_ROOM_ROLE_GUEST, true},
		{"moderator kicks moderator", commonv1.RoomRole_ROOM_ROLE_MODERATOR, commonv1.RoomRole_ROOM_ROLE_MODERATOR, false},
		{"moderator kicks owner", commonv1.RoomRole_ROOM_ROLE_MODERATOR, commonv1.RoomRole_ROOM_ROLE_OWNER, false},
		{"owner kicks owner", commonv1.RoomRole_ROOM_ROLE_OWNER, commonv1.RoomRole_ROOM_ROLE_OWNER, false},
		{"member kicks member", commonv1.RoomRole_ROOM_ROLE_MEMBER, commonv1.RoomRole_ROOM_ROLE_MEMBER, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CanKick(tc.byRole, tc.targetRole); got != tc.want {
				t.Errorf("CanKick(%v, %v) = %v, want %v", tc.byRole, tc.targetRole, got, tc.want)
			}
		})
	}
}

func TestCanPromoteMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		byRole  commonv1.RoomRole
		newRole commonv1.RoomRole
		want    bool
	}{
		{"owner promotes to moderator", commonv1.RoomRole_ROOM_ROLE_OWNER, commonv1.RoomRole_ROOM_ROLE_MODERATOR, true},
		{"owner promotes to member", commonv1.RoomRole_ROOM_ROLE_OWNER, commonv1.RoomRole_ROOM_ROLE_MEMBER, true},
		{"owner promotes to guest", commonv1.RoomRole_ROOM_ROLE_OWNER, commonv1.RoomRole_ROOM_ROLE_GUEST, true},
		{"owner tries promote to owner", commonv1.RoomRole_ROOM_ROLE_OWNER, commonv1.RoomRole_ROOM_ROLE_OWNER, false},
		{"moderator tries promote", commonv1.RoomRole_ROOM_ROLE_MODERATOR, commonv1.RoomRole_ROOM_ROLE_MODERATOR, false},
		{"member tries promote", commonv1.RoomRole_ROOM_ROLE_MEMBER, commonv1.RoomRole_ROOM_ROLE_MODERATOR, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CanPromote(tc.byRole, tc.newRole); got != tc.want {
				t.Errorf("CanPromote(%v, %v) = %v, want %v", tc.byRole, tc.newRole, got, tc.want)
			}
		})
	}
}

func TestGenerateInviteCodeIsUniqueAndLong(t *testing.T) {
	t.Parallel()
	c1 := GenerateInviteCode()
	c2 := GenerateInviteCode()
	if c1 == c2 {
		t.Error("invite codes should be random")
	}
	if len(c1) < 16 {
		t.Errorf("invite code too short: %d chars", len(c1))
	}
}
