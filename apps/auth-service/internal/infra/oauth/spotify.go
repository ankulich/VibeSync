package oauth

import (
	"encoding/json"
	"errors"

	"vibesync/apps/auth-service/internal/domain"
	"vibesync/apps/auth-service/internal/ports"
)

// Spotify endpoints. Spotify's OAuth2 implementation is largely standard but
// its userinfo endpoint is /v1/me (returns the user profile for the access
// token in the Authorization header).
const (
	spotifyAuthURL    = "https://accounts.spotify.com/authorize"
	spotifyTokenURL   = "https://accounts.spotify.com/api/token"
	spotifyProfileURL = "https://api.spotify.com/v1/me"
)

// SpotifyProvider implements ports.OAuthProvider for Spotify.
type SpotifyProvider struct {
	baseProvider
}

// NewSpotifyProvider constructs a SpotifyProvider from a Config. The Config
// must have ClientID and ClientSecret populated (from VB_OAUTH_SPOTIFY_* env);
// empty credentials produce a provider whose AuthorizationURL works but whose
// Exchange will fail at Spotify's token endpoint — that's the expected state
// when credentials aren't configured locally.
func NewSpotifyProvider(cfg Config) *SpotifyProvider {
	cfg.Name = "spotify"
	cfg.AuthURL = spotifyAuthURL
	cfg.TokenURL = spotifyTokenURL
	cfg.ProfileURL = spotifyProfileURL
	cfg.MapProfile = mapSpotifyProfile
	return &SpotifyProvider{baseProvider: newBase(cfg)}
}

// spotifyProfileJSON is the subset of fields Spotify returns from /v1/me that
// we care about for account creation. Images is an array of {url}; we pick the
// first as the avatar.
type spotifyProfileJSON struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	// Product is the subscription tier ("premium", "free", etc.). We do not
	// gate on it today but capture it for future "premium required for sync"
	// logic.
	Product string `json:"product"`
	Images  []struct {
		URL string `json:"url"`
	} `json:"images"`
}

func mapSpotifyProfile(raw []byte) (domain.ProviderProfile, error) {
	var p spotifyProfileJSON
	if err := json.Unmarshal(raw, &p); err != nil {
		return domain.ProviderProfile{}, errors.New("spotify: parse profile: " + err.Error())
	}
	avatar := ""
	if len(p.Images) > 0 {
		avatar = p.Images[0].URL
	}
	return domain.ProviderProfile{
		Provider:       "spotify",
		ProviderUserID: p.ID,
		Email:          p.Email,
		EmailVerified:  true, // Spotify verifies email at signup; we trust it.
		DisplayName:    p.DisplayName,
		AvatarURL:      avatar,
	}, nil
}

// Compile-time interface check.
var _ ports.OAuthProvider = (*SpotifyProvider)(nil)
