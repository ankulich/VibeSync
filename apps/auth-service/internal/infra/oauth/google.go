package oauth

import (
	"encoding/json"
	"errors"

	"vibesync/apps/auth-service/internal/domain"
	"vibesync/apps/auth-service/internal/ports"
)

// Google endpoints. Google speaks OpenID Connect; the userinfo endpoint
// returns the verified email when the openid+email+profile scopes are granted.
const (
	googleAuthURL    = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL   = "https://oauth2.googleapis.com/token"
	googleProfileURL = "https://openidconnect.googleapis.com/v1/userinfo"
)

// GoogleProvider implements ports.OAuthProvider for Google (YouTube). Uses
// the userinfo endpoint rather than decoding the id_token inline — verifying
// id_tokens requires Google's JWKS (fetched on demand) and the userinfo
// endpoint already does that work server-side. The id_token is still captured
// in ProviderTokens for clients that want to verify it themselves.
type GoogleProvider struct {
	baseProvider
}

// NewGoogleProvider constructs a GoogleProvider from a Config.
func NewGoogleProvider(cfg Config) *GoogleProvider {
	cfg.Name = "google"
	cfg.AuthURL = googleAuthURL
	cfg.TokenURL = googleTokenURL
	cfg.ProfileURL = googleProfileURL
	cfg.MapProfile = mapGoogleProfile
	return &GoogleProvider{baseProvider: newBase(cfg)}
}

// googleProfileJSON is the subset of fields Google's userinfo endpoint returns
// that we care about for account creation.
type googleProfileJSON struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

func mapGoogleProfile(raw []byte) (domain.ProviderProfile, error) {
	var p googleProfileJSON
	if err := json.Unmarshal(raw, &p); err != nil {
		return domain.ProviderProfile{}, errors.New("google: parse profile: " + err.Error())
	}
	return domain.ProviderProfile{
		Provider:       "google",
		ProviderUserID: p.Sub,
		Email:          p.Email,
		EmailVerified:  p.EmailVerified,
		DisplayName:    p.Name,
		AvatarURL:      p.Picture,
	}, nil
}

var _ ports.OAuthProvider = (*GoogleProvider)(nil)
