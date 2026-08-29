package domain

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"
)

// OAuthFlow is the transient server-side state for an in-progress OAuth2
// Authorization Code + PKCE flow. It bridges BeginOAuth (which issues state +
// stores code_challenge) and CompleteOAuth (which consumes state and verifies
// code_verifier).
//
// Why server-side state? Keeping the PKCE challenge on the server (rather than
// in a client-set cookie) means a stolen authorization code cannot be
// redeemed without also possessing server state — defense in depth beyond
// PKCE alone. State rows expire after OAuthFlowTTL (default 10m).
type OAuthFlow struct {
	State         string // PKCE + flow correlation key; also the OAuth "state" param
	Provider      string
	RedirectURI   string
	CodeChallenge string // base64url SHA256 of code_verifier, S256
	UserAgent     string // recorded for the session audit trail
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

// ErrFlowNotFound is returned when a flow state cannot be found, usually
// because it expired or was already consumed.
var ErrFlowNotFound = errors.New("oauth flow: not found")

// ErrFlowExpired is returned when a flow state exists but has passed its TTL.
var ErrFlowExpired = errors.New("oauth flow: expired")

// ErrFlowAlreadyUsed is returned when a flow state has already been consumed
// by a CompleteOAuth call. Each state is single-use.
var ErrFlowAlreadyUsed = errors.New("oauth flow: already used")

// stateBytes is the entropy in an OAuth state value. 16 bytes (128 bits) is
// well beyond any feasible brute-force; base64url-encoded to 22 chars.
const stateBytes = 16

// NewOAuthFlow constructs a fresh flow with a random state and the given TTL.
// codeChallenge is the client-supplied S256 challenge (may be empty if the
// client opted out of PKCE — not recommended but tolerated for non-SPA flows).
func NewOAuthFlow(now time.Time, provider, redirectURI, codeChallenge, userAgent string, ttl time.Duration) (OAuthFlow, error) {
	state, err := randomURL(stateBytes)
	if err != nil {
		return OAuthFlow{}, err
	}
	return OAuthFlow{
		State:         state,
		Provider:      provider,
		RedirectURI:   redirectURI,
		CodeChallenge: codeChallenge,
		UserAgent:     userAgent,
		CreatedAt:     now,
		ExpiresAt:     now.Add(ttl),
	}, nil
}

// IsExpired reports whether the flow has passed its TTL.
func (f OAuthFlow) IsExpired(now time.Time) bool {
	return !now.Before(f.ExpiresAt)
}

// randomURL is shared with refresh_token.go; declared once in this package.
// Re-declaring would be a compile error, so this is a no-op comment to
// document the provenance: the function lives in refresh_token.go.

// Ensure state encoding matches validator/selector encoding. We reuse the
// base64 RawURLEncoding from refresh_token.go's randomURL. To avoid a
// duplicate symbol, OAuthFlow uses randomURL directly.

// OAuthAccount is the durable link between a VibeSync user and an external
// provider identity. One user may have multiple links (Spotify + Google); a
// given (provider, provider_user_id) pair links to exactly one user.
type OAuthAccount struct {
	UserID         string
	Provider       string
	ProviderUserID string // the provider's stable subject/id for the user
	CreatedAt      time.Time
}

// ProviderProfile is the normalized profile fetched from a provider after a
// successful authorization-code exchange. The OAuthProvider port returns this;
// the use case upserts a user from it.
type ProviderProfile struct {
	Provider       string
	ProviderUserID string
	Email          string
	EmailVerified  bool
	DisplayName    string
	AvatarURL      string
}

// EncodeState is exported for tests that need to construct known state values.
// Production code uses NewOAuthFlow, which calls randomURL internally.
func EncodeState(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// RandomBytes returns n random bytes, exported for tests that need raw
// entropy (e.g. to construct a state value then EncodeState it).
func RandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}
