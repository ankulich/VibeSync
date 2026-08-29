package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"vibesync/apps/auth-service/internal/infra/postgres"
	authv1 "vibesync/gen/go/vibesync/auth/v1"
	vberr "vibesync/libs/errors"
)

// tokenRefreshMargin is how long before expiry we proactively refresh.
const tokenRefreshMargin = 2 * time.Minute

// GetProviderToken returns a fresh provider access token for a user. If the
// stored token is expired (or close to expiring), it is refreshed via the
// provider's token endpoint using the stored refresh_token, and the stored
// pair is updated before returning. Internal-use (Provider Service).
func (s *Service) GetProviderToken(
	ctx context.Context,
	req *connect.Request[authv1.GetProviderTokenRequest],
) (*connect.Response[authv1.GetProviderTokenResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	userID := req.Msg.GetUserId().GetValue()
	providerName := req.Msg.GetProvider()
	if userID == "" || providerName == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.auth", "MISSING_PARAMS", "user_id and provider required")
	}
	if s.registry == nil {
		return nil, vberr.FailedPrecondition("vibesync.auth", "OAUTH_DISABLED", "no OAuth providers configured")
	}

	// Verify the provider is known.
	if _, ok := s.registry.Get(providerName); !ok {
		return nil, vberr.InvalidArgumentFor("vibesync.auth", "UNKNOWN_PROVIDER", "unknown provider: "+providerName)
	}

	// Load the stored token.
	var stored postgres.ProviderToken
	err := s.readTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var ferr error
		stored, ferr = postgres.NewProviderTokenRepo().Get(ctx, tx, providerName, userID)
		return ferr
	})
	if err != nil {
		if isNotFound(err) {
			return nil, vberr.NotFound("provider_token", providerName+":"+userID)
		}
		return nil, vberr.Internal("TOKEN_FETCH_FAILED", err.Error()).WithCause(err)
	}

	// Decrypt.
	accessToken, err := s.cipher.Decrypt(stored.AccessTokenEnc)
	if err != nil {
		return nil, vberr.Internal("TOKEN_DECRYPT_FAILED", err.Error()).WithCause(err)
	}
	refreshToken, err := s.cipher.Decrypt(stored.RefreshTokenEnc)
	if err != nil {
		return nil, vberr.Internal("TOKEN_DECRYPT_FAILED", err.Error()).WithCause(err)
	}

	// If not expired (with margin), return as-is.
	now := s.now()
	if now.Before(stored.ExpiresAt.Add(-tokenRefreshMargin)) {
		return connect.NewResponse(&authv1.GetProviderTokenResponse{
			AccessToken: string(accessToken),
			ExpiresAt:   timestamppb.New(stored.ExpiresAt),
		}), nil
	}

	// Refresh via the provider's token endpoint.
	newAccess, newRefresh, newExpiry, err := s.refreshProviderToken(ctx, providerName, string(refreshToken))
	if err != nil {
		return nil, vberr.Internal("TOKEN_REFRESH_FAILED", err.Error()).WithCause(err)
	}

	// Store the refreshed pair.
	encAccess, err := s.cipher.Encrypt([]byte(newAccess))
	if err != nil {
		return nil, vberr.Internal("TOKEN_ENCRYPT_FAILED", err.Error()).WithCause(err)
	}
	encRefresh, err := s.cipher.Encrypt([]byte(newRefresh))
	if err != nil {
		return nil, vberr.Internal("TOKEN_ENCRYPT_FAILED", err.Error()).WithCause(err)
	}
	err = s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return postgres.NewProviderTokenRepo().Upsert(ctx, tx, postgres.ProviderToken{
			Provider: providerName, UserID: userID,
			AccessTokenEnc: encAccess, RefreshTokenEnc: encRefresh,
			ExpiresAt: newExpiry,
		})
	})
	if err != nil {
		return nil, vberr.Internal("TOKEN_STORE_FAILED", err.Error()).WithCause(err)
	}

	return connect.NewResponse(&authv1.GetProviderTokenResponse{
		AccessToken: newAccess,
		ExpiresAt:   timestamppb.New(newExpiry),
	}), nil
}

// providerTokenEndpoints maps provider names to their token-refresh endpoints.
var providerTokenEndpoints = map[string]string{
	"spotify": "https://accounts.spotify.com/api/token",
	"google":  "https://oauth2.googleapis.com/token",
}

// providerClientCreds returns the client_id/client_secret for a provider,
// resolved from the auth service's OAuth config.
func (s *Service) providerClientCreds(provider string) (clientID, clientSecret string, ok bool) {
	switch provider {
	case "spotify":
		return s.cfg.OAuth.Spotify.ClientIDEnv, s.cfg.OAuth.Spotify.ClientSecretEnv, true
	case "google":
		return s.cfg.OAuth.Google.ClientIDEnv, s.cfg.OAuth.Google.ClientSecretEnv, true
	}
	return "", "", false
}

// tokenRefreshResponse is the standard OAuth2 token-refresh JSON response.
type tokenRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// refreshProviderToken exchanges a refresh_token for a new token pair via the
// provider's token endpoint (standard OAuth2 refresh_token grant).
func (s *Service) refreshProviderToken(ctx context.Context, provider, refreshToken string) (string, string, time.Time, error) {
	clientIDEnv, clientSecretEnv, ok := s.providerClientCreds(provider)
	if !ok {
		return "", "", time.Time{}, fmt.Errorf("no client credentials for provider %q", provider)
	}
	// The config fields hold env-var NAMES for the credentials. The actual
	// values are read from the environment at the use site in main.go; here
	// we read them directly since the OAuth config may have been resolved
	// already in some deployments. We use os.Getenv as the canonical path.
	clientID := resolveEnvOrValue(clientIDEnv)
	clientSecret := resolveEnvOrValue(clientSecretEnv)

	endpoint, ok := providerTokenEndpoints[provider]
	if !ok {
		return "", "", time.Time{}, fmt.Errorf("no token endpoint for provider %q", provider)
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	if clientID != "" {
		form.Set("client_id", clientID)
		form.Set("client_secret", clientSecret)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", time.Time{}, err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", time.Time{}, fmt.Errorf("refresh status %s", resp.Status)
	}

	var tr tokenRefreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", "", time.Time{}, fmt.Errorf("decode refresh response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", "", time.Time{}, errors.New("empty access token in refresh response")
	}
	// Some providers don't return a new refresh_token; reuse the old one.
	if tr.RefreshToken == "" {
		tr.RefreshToken = refreshToken
	}
	expiresAt := s.now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return tr.AccessToken, tr.RefreshToken, expiresAt, nil
}

// resolveEnvOrValue returns os.Getenv(name) if non-empty, else the value
// itself (for configs that store the literal credential rather than an
// env-var name).
func resolveEnvOrValue(s string) string {
	if v := os.Getenv(s); v != "" {
		return v
	}
	return s
}
