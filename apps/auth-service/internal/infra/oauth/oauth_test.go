package oauth

import (
	"context"
	"testing"

	"vibesync/apps/auth-service/internal/domain"
	"vibesync/apps/auth-service/internal/ports"
)

func TestMapSpotifyProfile(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"id": "spotify_user_1",
		"display_name": "Alice",
		"email": "alice@example.com",
		"product": "premium",
		"images": [{"url": "https://i.scdn.co/avatar.png"}]
	}`)
	p, err := mapSpotifyProfile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Provider != "spotify" {
		t.Errorf("Provider = %q, want spotify", p.Provider)
	}
	if p.ProviderUserID != "spotify_user_1" {
		t.Errorf("ProviderUserID = %q", p.ProviderUserID)
	}
	if p.Email != "alice@example.com" {
		t.Errorf("Email = %q", p.Email)
	}
	if !p.EmailVerified {
		t.Error("Spotify email should be treated as verified")
	}
	if p.AvatarURL != "https://i.scdn.co/avatar.png" {
		t.Errorf("AvatarURL = %q", p.AvatarURL)
	}
}

func TestMapSpotifyProfileNoImages(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"id":"u","display_name":"A","email":"a@b.co"}`)
	p, err := mapSpotifyProfile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.AvatarURL != "" {
		t.Errorf("AvatarURL should be empty when no images; got %q", p.AvatarURL)
	}
}

func TestMapSpotifyProfileRejectsMalformed(t *testing.T) {
	t.Parallel()
	if _, err := mapSpotifyProfile([]byte(`{not json`)); err == nil {
		t.Error("malformed JSON must produce an error")
	}
}

func TestMapGoogleProfile(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"sub": "google_user_1",
		"email": "bob@example.com",
		"email_verified": true,
		"name": "Bob",
		"picture": "https://lh3.googleusercontent.com/bob.png"
	}`)
	p, err := mapGoogleProfile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Provider != "google" {
		t.Errorf("Provider = %q, want google", p.Provider)
	}
	if p.ProviderUserID != "google_user_1" {
		t.Errorf("ProviderUserID = %q", p.ProviderUserID)
	}
	if !p.EmailVerified {
		t.Error("Google email_verified=true must propagate")
	}
}

func TestMapGoogleProfileUnverifiedEmail(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"sub":"g","email":"a@b.co","email_verified":false}`)
	p, _ := mapGoogleProfile(raw)
	if p.EmailVerified {
		t.Error("email_verified=false must propagate as false")
	}
}

func TestRegistryGet(t *testing.T) {
	t.Parallel()
	r := NewRegistry(stubProvider("spotify"), stubProvider("google"))
	if _, ok := r.Get("spotify"); !ok {
		t.Error("spotify must be registered")
	}
	if _, ok := r.Get("unknown"); ok {
		t.Error("unknown provider must not be found")
	}
	if len(r.Names()) != 2 {
		t.Errorf("Names() = %d, want 2", len(r.Names()))
	}
}

// stubProvider is a no-op OAuthProvider for registry tests. Its methods are
// never called by the registry; only Name() is.
type stubProvider string

func (s stubProvider) Name() string { return string(s) }
func (stubProvider) AuthorizationURL(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}
func (stubProvider) Exchange(_ context.Context, _, _, _ string) (ports.ProviderTokens, error) {
	return ports.ProviderTokens{}, nil
}
func (stubProvider) Profile(_ context.Context, _ ports.ProviderTokens) (domain.ProviderProfile, error) {
	return domain.ProviderProfile{}, nil
}
