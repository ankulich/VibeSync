package domain

import (
	"errors"
	"strings"
	"testing"
	"time"

	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

func TestNewUserValidates(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		params  NewUserParams
		wantErr error
	}{
		{"happy path", NewUserParams{Email: "alice@example.com", Username: "alice"}, nil},
		{"bad email - no at", NewUserParams{Email: "aliceexample.com", Username: "alice"}, ErrEmailInvalid},
		{"bad email - empty", NewUserParams{Email: "", Username: "alice"}, ErrEmailInvalid},
		{"username too short", NewUserParams{Email: "a@b.co", Username: "ab"}, ErrUsernameInvalid},
		// Uppercase is normalized to lowercase by NewUser, so "Alice" is valid.
		{"username starts with digit", NewUserParams{Email: "a@b.co", Username: "1alice"}, ErrUsernameInvalid},
		{"username has space", NewUserParams{Email: "a@b.co", Username: "alice bob"}, ErrUsernameInvalid},
		{"display name too long", NewUserParams{Email: "a@b.co", Username: "alice", DisplayName: strings.Repeat("x", 101)}, ErrDisplayNameTooLong},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u, err := NewUser(now, tc.params)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("expected %v, got nil", tc.wantErr)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if u.ID != "" {
				t.Errorf("ID should be empty (assigned by repo); got %q", u.ID)
			}
			if u.Email != strings.ToLower(tc.params.Email) {
				t.Errorf("email should be lowercased; got %q", u.Email)
			}
			if u.SystemRole != commonv1.SystemRole_SYSTEM_ROLE_USER {
				t.Errorf("default role should be USER; got %v", u.SystemRole)
			}
			if u.Status != UserStatusActive {
				t.Errorf("default status should be Active; got %v", u.Status)
			}
			if u.CreatedAt != now {
				t.Errorf("CreatedAt should be now")
			}
		})
	}
}

func TestNewUserDefaultsDisplayName(t *testing.T) {
	t.Parallel()
	u, err := NewUser(time.Now(), NewUserParams{Email: "a@b.co", Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if u.DisplayName != "alice" {
		t.Errorf("empty display name should default to username; got %q", u.DisplayName)
	}
}

func TestNewUserNormalizesCase(t *testing.T) {
	t.Parallel()
	u, err := NewUser(time.Now(), NewUserParams{Email: "Alice@Example.COM", Username: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "alice@example.com" {
		t.Errorf("email should be lowercased; got %q", u.Email)
	}
	if u.Username != "alice" {
		t.Errorf("username should be lowercased; got %q", u.Username)
	}
}

func TestUserSetPasswordAndHasPassword(t *testing.T) {
	t.Parallel()
	u, _ := NewUser(time.Now(), NewUserParams{Email: "a@b.co", Username: "alice"})
	if u.HasPassword() {
		t.Error("new user should not have a password")
	}
	u.SetPassword("$argon2id$...")
	if !u.HasPassword() {
		t.Error("after SetPassword the user should report HasPassword")
	}
}

func TestUserStatusCanLogin(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status UserStatus
		want   bool
	}{
		{UserStatusActive, true},
		{UserStatusSuspended, false},
		{UserStatusDeleted, false},
		{UserStatusUnspecified, false},
	}
	for _, tc := range cases {
		if got := tc.status.CanLogin(); got != tc.want {
			t.Errorf("%v.CanLogin() = %v, want %v", tc.status, got, tc.want)
		}
	}
}

// (errorIs helper removed — the stdlib errors.Is is used directly now.)
