// Package oauth implements the OAuth2 provider abstraction and concrete
// Spotify + Google clients. See ADR (Phase 4 decisions): the abstraction is
// the contract every provider satisfies; concrete clients need real client
// IDs/secrets at runtime via VB_OAUTH_* env vars.
//
// All flows are Authorization Code + PKCE (S256). PKCE is mandatory even for
// server-side flows because it costs nothing and defends against
// authorization-code interception.
package oauth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"vibesync/apps/auth-service/internal/domain"
	"vibesync/apps/auth-service/internal/ports"
)

// Config is the per-provider configuration: credentials, scopes, redirect.
// The Registry holds one Config per provider name.
type Config struct {
	Name         string
	ClientID     string
	ClientSecret string
	Scopes       []string
	RedirectURL  string // full callback URL, e.g. https://api.vibesync.example/api/v1/oauth/callback
	// AuthURL and TokenURL are provider-specific endpoints, set by each
	// concrete client's constructor.
	AuthURL  string
	TokenURL string
	// ProfileURL is the provider's user-info endpoint. ProfileFetch lets a
	// client customize the request (e.g. extra headers) when the simple
	// "Bearer token GET" pattern does not apply.
	ProfileURL string
	// ProfileFetch, if non-nil, fetches the provider profile from the token.
	// Defaults to defaultProfileFetch (Bearer GET ProfileURL, then MapProfile).
	ProfileFetch func(ctx context.Context, tokens ports.ProviderTokens, cfg Config) (domain.ProviderProfile, error)
	// MapProfile converts the provider's raw userinfo JSON into the normalized
	// ProviderProfile. Each concrete provider implements this; the JSON shape
	// differs (Spotify uses snake_case top-level fields, Google uses OpenID
	// Connect claims).
	MapProfile func(raw []byte) (domain.ProviderProfile, error)
}

// baseProvider is the shared Authorization Code + PKCE machinery. Concrete
// providers (spotify, google) embed it and supply their Config.
type baseProvider struct {
	cfg      Config
	oauthCfg oauth2.Config
	http     *http.Client
}

func newBase(cfg Config) baseProvider {
	return baseProvider{
		cfg: cfg,
		oauthCfg: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint: oauth2.Endpoint{
				AuthURL:  cfg.AuthURL,
				TokenURL: cfg.TokenURL,
			},
			RedirectURL: cfg.RedirectURL,
			Scopes:      cfg.Scopes,
		},
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

// Name returns the provider's stable identifier.
func (b baseProvider) Name() string { return b.cfg.Name }

// AuthorizationURL builds the provider's auth URL with state + PKCE.
// codeChallenge is the S256 challenge the client generated; the verifier
// stays client-side and is sent to Exchange.
func (b baseProvider) AuthorizationURL(_ context.Context, _ /*redirectURI*/, state, codeChallenge string) (string, error) {
	// golang.org/x/oauth2 does not surface PKCE on Config.AuthCodeURL cleanly;
	// we pass it via CodeChallengeMethod + CodeChallenge params.
	return b.oauthCfg.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	), nil
}

// Exchange swaps an authorization code for provider tokens. PKCE verifier is
// sent so the provider can confirm the original challenge.
func (b baseProvider) Exchange(ctx context.Context, code, _ /*redirectURI*/, codeVerifier string) (ports.ProviderTokens, error) {
	var opts []oauth2.AuthCodeOption
	if codeVerifier != "" {
		opts = append(opts,
			oauth2.SetAuthURLParam("code_verifier", codeVerifier),
		)
	}
	tok, err := b.oauthCfg.Exchange(ctx, code, opts...)
	if err != nil {
		return ports.ProviderTokens{}, err
	}
	return providerTokensFromOauth2(tok), nil
}

// Profile fetches the user profile using the provider tokens. Dispatches to
// the per-provider ProfileFetch if set, else the default GET-with-bearer flow.
func (b baseProvider) Profile(ctx context.Context, tokens ports.ProviderTokens) (domain.ProviderProfile, error) {
	if b.cfg.ProfileFetch != nil {
		return b.cfg.ProfileFetch(ctx, tokens, b.cfg)
	}
	return defaultProfileFetch(ctx, tokens, b.cfg, b.http)
}

func providerTokensFromOauth2(t *oauth2.Token) ports.ProviderTokens {
	out := ports.ProviderTokens{AccessToken: t.AccessToken}
	if v, ok := t.Extra("id_token").(string); ok {
		out.IDToken = v
	}
	if v, ok := t.Extra("refresh_token").(string); ok {
		out.RefreshToken = v
	}
	if !t.Expiry.IsZero() {
		out.ExpiresIn = time.Until(t.Expiry)
	}
	return out
}

// defaultProfileFetch is the simple GET-ProfileURL-with-Bearer flow. Used by
// providers whose userinfo endpoint follows the standard pattern (Spotify).
// Google overrides via ProfileFetch to verify the id_token (OIDC).
func defaultProfileFetch(ctx context.Context, tokens ports.ProviderTokens, cfg Config, hc *http.Client) (domain.ProviderProfile, error) {
	if cfg.ProfileURL == "" || cfg.MapProfile == nil {
		return domain.ProviderProfile{}, errors.New("oauth: profile URL or mapper missing")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.ProfileURL, nil)
	if err != nil {
		return domain.ProviderProfile{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return domain.ProviderProfile{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return domain.ProviderProfile{}, errors.New("oauth: profile fetch status " + resp.Status)
	}
	raw, err := ioReadAll(resp.Body)
	if err != nil {
		return domain.ProviderProfile{}, err
	}
	return cfg.MapProfile(raw)
}

// Registry resolves provider implementations by name. The use case consults
// the registry on BeginOAuth/CompleteOAuth.
type Registry struct {
	providers map[string]ports.OAuthProvider
}

// NewRegistry constructs a Registry from the configured providers. Disabled
// providers are omitted from the map; BeginOAuth for an unregistered name
// returns InvalidArgument.
func NewRegistry(providers ...ports.OAuthProvider) *Registry {
	r := &Registry{providers: make(map[string]ports.OAuthProvider, len(providers))}
	for _, p := range providers {
		r.providers[p.Name()] = p
	}
	return r
}

// Get returns the named provider. ok=false if the name is unknown or disabled.
func (r *Registry) Get(name string) (ports.OAuthProvider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

// Names returns the registered provider names (for the use case to surface in
// error messages and the future "list providers" endpoint).
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.providers))
	for k := range r.providers {
		out = append(out, k)
	}
	return out
}
