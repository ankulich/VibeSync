package domain

import (
	"errors"
	"testing"
	"time"

	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

func TestProjectFromEventHappyPath(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	event := UserCreatedV1{
		UserID:    "01J6USER000000000000000A",
		Email:     "Alice@Example.COM",
		Username:  "Alice",
		Provider:  "spotify",
		CreatedAt: "2026-08-03T10:30:00Z",
	}
	user, err := ProjectFromEvent(event, now)
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "01J6USER000000000000000A" {
		t.Errorf("ID = %q", user.ID)
	}
	if user.Email != "alice@example.com" {
		t.Errorf("Email should be lowercased; got %q", user.Email)
	}
	if user.Username != "alice" {
		t.Errorf("Username should be lowercased; got %q", user.Username)
	}
	if user.DisplayName != "alice" {
		t.Errorf("DisplayName should default to username; got %q", user.DisplayName)
	}
	if user.AvatarURL != "" {
		t.Errorf("AvatarURL should be empty; got %q", user.AvatarURL)
	}
	if user.SystemRole != commonv1.SystemRole_SYSTEM_ROLE_USER {
		t.Errorf("SystemRole should default to USER; got %v", user.SystemRole)
	}
	wantCreated := time.Date(2026, 8, 3, 10, 30, 0, 0, time.UTC)
	if !user.CreatedAt.Equal(wantCreated) {
		t.Errorf("CreatedAt = %v, want %v", user.CreatedAt, wantCreated)
	}
	if !user.UpdatedAt.Equal(wantCreated) {
		t.Errorf("UpdatedAt should equal CreatedAt on projection; got %v", user.UpdatedAt)
	}
}

func TestProjectFromEventMissingFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		event UserCreatedV1
	}{
		{"missing user_id", UserCreatedV1{Email: "a@b.co", Username: "a"}},
		{"missing email", UserCreatedV1{UserID: "u1", Username: "a"}},
		{"missing username", UserCreatedV1{UserID: "u1", Email: "a@b.co"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ProjectFromEvent(tc.event, time.Now())
			if !errors.Is(err, ErrInvalidEvent) {
				t.Errorf("expected ErrInvalidEvent, got %v", err)
			}
		})
	}
}

func TestProjectFromEventBadTimestampFallsBack(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	event := UserCreatedV1{
		UserID:    "u1",
		Email:     "a@b.co",
		Username:  "a",
		CreatedAt: "not-a-date",
	}
	user, err := ProjectFromEvent(event, now)
	if err != nil {
		t.Fatal(err)
	}
	if !user.CreatedAt.Equal(now) {
		t.Errorf("bad timestamp should fall back to now; got %v", user.CreatedAt)
	}
}

func TestProjectFromEventEmptyTimestampFallsBack(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	user, err := ProjectFromEvent(UserCreatedV1{
		UserID: "u1", Email: "a@b.co", Username: "a",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !user.CreatedAt.Equal(now) {
		t.Errorf("empty timestamp should fall back to now; got %v", user.CreatedAt)
	}
}

func TestApplyUpdateDisplayName(t *testing.T) {
	t.Parallel()
	u := User{ID: "u1", DisplayName: "old", AvatarURL: "old.png"}
	newName := "New Name"
	u.ApplyUpdate(time.Now(), &newName, nil)
	if u.DisplayName != "New Name" {
		t.Errorf("DisplayName = %q, want 'New Name'", u.DisplayName)
	}
	if u.AvatarURL != "old.png" {
		t.Errorf("AvatarURL should be unchanged when nil; got %q", u.AvatarURL)
	}
}

func TestApplyUpdateAvatarURL(t *testing.T) {
	t.Parallel()
	u := User{ID: "u1", DisplayName: "name", AvatarURL: "old.png"}
	newURL := "new.png"
	u.ApplyUpdate(time.Now(), nil, &newURL)
	if u.AvatarURL != "new.png" {
		t.Errorf("AvatarURL = %q", u.AvatarURL)
	}
	if u.DisplayName != "name" {
		t.Errorf("DisplayName should be unchanged when nil; got %q", u.DisplayName)
	}
}

func TestApplyUpdateEmptyDisplayNameIgnored(t *testing.T) {
	t.Parallel()
	u := User{ID: "u1", DisplayName: "keep"}
	empty := "  "
	u.ApplyUpdate(time.Now(), &empty, nil)
	if u.DisplayName != "keep" {
		t.Errorf("empty/whitespace display name should be ignored; got %q", u.DisplayName)
	}
}

func TestApplyUpdateBothNil(t *testing.T) {
	t.Parallel()
	u := User{ID: "u1", DisplayName: "name", AvatarURL: "url.png", UpdatedAt: time.Time{}}
	before := u.UpdatedAt
	u.ApplyUpdate(time.Now(), nil, nil)
	if u.UpdatedAt.Equal(before) {
		t.Error("UpdatedAt should be refreshed even when no fields change")
	}
}
