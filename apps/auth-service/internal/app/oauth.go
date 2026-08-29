package app

import (
	"context"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	"vibesync/apps/auth-service/internal/domain"
	"vibesync/apps/auth-service/internal/infra/postgres"
	"vibesync/apps/auth-service/internal/ports"
	authv1 "vibesync/gen/go/vibesync/auth/v1"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	vberr "vibesync/libs/errors"
	vboutbox "vibesync/libs/outbox"
)

// BeginOAuth starts an OAuth2 Authorization Code + PKCE flow. Stores the flow
// state (provider, redirect, code_challenge, user-agent) keyed by a random
// `state` value, and returns the provider's authorization URL for the client
// to redirect to.
//
// The client MUST round-trip the returned `state` on CompleteOAuth; the server
// matches it to the stored flow and consumes it (single-use).
func (s *Service) BeginOAuth(ctx context.Context, req *connect.Request[authv1.BeginOAuthRequest]) (*connect.Response[authv1.BeginOAuthResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	providerName := req.Msg.GetProvider()
	redirectURI := req.Msg.GetRedirectUri()
	if providerName == "" || redirectURI == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.auth", "MISSING_OAUTH_PARAMS", "provider and redirect_uri required")
	}
	if s.registry == nil {
		return nil, vberr.FailedPrecondition("vibesync.auth", "OAUTH_DISABLED", "no OAuth providers configured")
	}
	provider, ok := s.registry.Get(providerName)
	if !ok {
		return nil, vberr.InvalidArgumentFor("vibesync.auth", "UNKNOWN_PROVIDER", "unknown or disabled OAuth provider: "+providerName)
	}

	now := s.now()
	flow, err := domain.NewOAuthFlow(now, providerName, redirectURI, req.Msg.GetCodeChallenge(), "", s.cfg.Auth.OAuthFlowTTL)
	if err != nil {
		return nil, vberr.Internal("OAUTH_FLOW_INIT", err.Error()).WithCause(err)
	}

	// Persist the flow state in its own short tx (no domain write to couple to).
	if err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return s.flows.Create(ctx, tx, flow)
	}); err != nil {
		return nil, err
	}

	// Build the provider authorization URL with state + PKCE challenge.
	authURL, err := provider.AuthorizationURL(ctx, redirectURI, flow.State, flow.CodeChallenge)
	if err != nil {
		return nil, vberr.Internal("OAUTH_AUTH_URL", err.Error()).WithCause(err)
	}
	return connect.NewResponse(&authv1.BeginOAuthResponse{
		AuthorizationUrl: authURL,
		State:            flow.State,
	}), nil
}

// CompleteOAuth finishes an OAuth2 flow: validates state, exchanges the code
// for provider tokens, fetches the profile, upserts the user + OAuth link, and
// issues a VibeSync token pair. The user is created on first OAuth login
// (ADR-0010) and a user.created.v1 event is staged in the outbox for the User
// Service read model.
func (s *Service) CompleteOAuth(ctx context.Context, req *connect.Request[authv1.CompleteOAuthRequest]) (*connect.Response[authv1.CompleteOAuthResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	if s.registry == nil {
		return nil, vberr.FailedPrecondition("vibesync.auth", "OAUTH_DISABLED", "no OAuth providers configured")
	}
	provider, ok := s.registry.Get(req.Msg.GetProvider())
	if !ok {
		return nil, vberr.InvalidArgumentFor("vibesync.auth", "UNKNOWN_PROVIDER", "unknown or disabled OAuth provider")
	}

	// Consume the flow state first (single-use, outside the upsert tx). The
	// flow row is deleted on read so a replayed CompleteOAuth fails here.
	var flow domain.OAuthFlow
	if err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		f, err := s.flows.GetAndConsume(ctx, tx, req.Msg.GetState())
		if err != nil {
			if isNotFound(err) {
				return vberr.InvalidArgumentFor("vibesync.auth", "BAD_STATE", "state not found or expired")
			}
			return err
		}
		flow = f
		return nil
	}); err != nil {
		return nil, err
	}

	// Exchange + profile fetch happen OUTSIDE any tx (network calls). A failure
	// here leaves the flow consumed — the client must BeginOAuth again. That's
	// correct: PKCE verifiers are one-shot.
	tokens, err := provider.Exchange(ctx, req.Msg.GetCode(), flow.RedirectURI, req.Msg.GetCodeVerifier())
	if err != nil {
		return nil, vberr.Unauthenticated("OAUTH_EXCHANGE_FAILED").WithCause(err)
	}
	profile, err := provider.Profile(ctx, tokens)
	if err != nil {
		return nil, vberr.Internal("OAUTH_PROFILE_FAILED", err.Error()).WithCause(err)
	}
	if profile.ProviderUserID == "" || profile.Email == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.auth", "OAUTH_PROFILE_INCOMPLETE", "provider did not return id and email")
	}

	// Upsert user + OAuth link + session + refresh + provider tokens, all in
	// one tx. Emit user.created.v1 only on first creation.
	session, material, err := s.completeOAuthUpsert(ctx, provider.Name(), profile, flow.UserAgent, tokens)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv1.CompleteOAuthResponse{
		Session: &authv1.AuthSession{
			Tokens: &authv1.TokenPair{
				AccessToken:  material.AccessToken,
				RefreshToken: material.RefreshToken,
				TokenType:    "Bearer",
				ExpiresIn:    material.ExpiresIn,
			},
			UserId: &commonv1.Id{Value: session.UserID},
		},
	}), nil
}

// completeOAuthUpsert is the tx-scoped upsert. Returns the new session + token
// material. Saves the provider tokens (encrypted) and emits user.created.v1 to
// the outbox when a fresh user is created.
func (s *Service) completeOAuthUpsert(ctx context.Context, providerName string, profile domain.ProviderProfile, deviceLabel string, tokens ports.ProviderTokens) (domain.Session, tokenMaterial, error) {
	var (
		session  domain.Session
		material tokenMaterial
	)
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var user domain.User

		// 1. Existing link? → load user.
		link, err := s.accounts.GetByProvider(ctx, tx, providerName, profile.ProviderUserID)
		switch {
		case err == nil:
			user, err = s.users.GetByID(ctx, tx, link.UserID)
			if err != nil {
				return err
			}
		case !isNotFound(err):
			return err
		default:
			// 2. No link. Existing user with the same email? → link.
			user, err = s.users.GetByEmail(ctx, tx, profile.Email)
			switch {
			case err == nil:
				if err := s.accounts.Upsert(ctx, tx, domain.OAuthAccount{
					UserID:         user.ID,
					Provider:       providerName,
					ProviderUserID: profile.ProviderUserID,
					CreatedAt:      s.now(),
				}); err != nil {
					return err
				}
			case !isNotFound(err):
				return err
			default:
				// 3. Brand-new user. Create + link + outbox event.
				now := s.now()
				newUser, nerr := domain.NewUser(now, domain.NewUserParams{
					Email:       profile.Email,
					Username:    deriveUsername(profile),
					DisplayName: profile.DisplayName,
					AvatarURL:   profile.AvatarURL,
				})
				if nerr != nil {
					return vberr.InvalidArgumentFor("vibesync.auth", "OAUTH_PROFILE_INVALID", nerr.Error()).WithCause(nerr)
				}
				newUser.ID = s.idgen.New()
				if err := s.users.Create(ctx, tx, newUser); err != nil {
					return err
				}
				if err := s.accounts.Upsert(ctx, tx, domain.OAuthAccount{
					UserID:         newUser.ID,
					Provider:       providerName,
					ProviderUserID: profile.ProviderUserID,
					CreatedAt:      now,
				}); err != nil {
					return err
				}
				// Stage user.created.v1 so the User Service (Phase 5) builds its
				// read model. Payload is a small JSON shape; the schema is fixed in
				// contracts/user-events.md (future).
				payload, _ := json.Marshal(map[string]any{
					"user_id":    newUser.ID,
					"email":      newUser.Email,
					"username":   newUser.Username,
					"provider":   providerName,
					"created_at": now.UTC().Format("2006-01-02T15:04:05Z07:00"),
				})
				if err := s.outbox.Append(ctx, tx, vboutbox.Event{
					ID:          s.idgen.New(),
					AggregateID: newUser.ID,
					Topic:       "user.created.v1",
					Key:         newUser.ID,
					Payload:     payload,
					OccurredAt:  now,
					Version:     "v1",
				}); err != nil {
					return err
				}
				user = newUser
			}
		}

		if !user.CanAuthenticate() {
			return vberr.PermissionDenied("oauth_login", "user:"+user.ID)
		}

		// Save the provider tokens (encrypted) so the Provider Service can
		// act on the user's behalf via GetProviderToken.
		if err := s.saveProviderTokens(ctx, tx, providerName, user.ID, tokens); err != nil {
			return err
		}

		return s.issueSessionForUser(ctx, tx, user, deviceLabel, &session, &material)
	})
	if err != nil {
		return domain.Session{}, tokenMaterial{}, err
	}
	return session, material, nil
}

// saveProviderTokens encrypts and stores the provider token pair for a user.
// Called inside the CompleteOAuth transaction. Tokens with an empty refresh
// token are skipped (some providers don't issue refresh tokens).
func (s *Service) saveProviderTokens(ctx context.Context, tx pgx.Tx, providerName, userID string, tokens ports.ProviderTokens) error {
	if tokens.AccessToken == "" {
		return nil // nothing to store
	}
	encAccess, err := s.cipher.Encrypt([]byte(tokens.AccessToken))
	if err != nil {
		return vberr.Internal("TOKEN_ENCRYPT_FAILED", err.Error()).WithCause(err)
	}
	// Some providers don't return a refresh token on every exchange; store an
	// empty encrypted blob to satisfy the NOT NULL constraint.
	refresh := tokens.RefreshToken
	encRefresh, err := s.cipher.Encrypt([]byte(refresh))
	if err != nil {
		return vberr.Internal("TOKEN_ENCRYPT_FAILED", err.Error()).WithCause(err)
	}
	expiresAt := s.now().Add(tokens.ExpiresIn)
	if tokens.ExpiresIn == 0 {
		expiresAt = s.now().Add(time.Hour) // sensible default if provider omits expiry
	}
	return postgres.NewProviderTokenRepo().Upsert(ctx, tx, postgres.ProviderToken{
		Provider:        providerName,
		UserID:          userID,
		AccessTokenEnc:  encAccess,
		RefreshTokenEnc: encRefresh,
		ExpiresAt:       expiresAt,
	})
}

// issueSessionForUser creates a fresh session + root refresh token for an
// existing-or-just-created user, shared by login and OAuth. Stages nothing in
// the outbox (callers stage their own events).
func (s *Service) issueSessionForUser(
	ctx context.Context,
	tx pgx.Tx,
	user domain.User,
	deviceLabel string,
	session *domain.Session,
	material *tokenMaterial,
) error {
	now := s.now()
	sessionID := s.idgen.New()
	familyID := s.idgen.New()
	sess := domain.NewSession(now, sessionID, user.ID, familyID, s.cfg.Auth.SessionTTL, deviceLabel)
	if err := s.sessions.Create(ctx, tx, sess); err != nil {
		return err
	}
	rtID := s.idgen.New()
	rt, validator, err := domain.NewRefreshToken(now, rtID, familyID, user.ID, sessionID, "", s.cfg.Auth.RefreshTokenTTL, s.hasher.Hash)
	if err != nil {
		return err
	}
	if err := s.refresh.Create(ctx, tx, rt); err != nil {
		return err
	}
	access, err := s.issueAccessToken(ctx, user)
	if err != nil {
		return err
	}
	*session = sess
	*material = tokenMaterial{
		AccessToken:  access,
		RefreshToken: validator.PresentationToken(),
		ExpiresIn:    uint64(s.cfg.Auth.AccessTokenTTL.Seconds()),
	}
	return nil
}

// deriveUsername produces a username from a provider profile when the provider
// doesn't return one. We use the email's local part; collisions are resolved
// by the UNIQUE constraint on users.username and surfaced as InvalidArgument
// (a future improvement would auto-suffix on conflict).
func deriveUsername(p domain.ProviderProfile) string {
	if p.Email == "" {
		return ""
	}
	local := p.Email
	for i := 0; i < len(local); i++ {
		if local[i] == '@' {
			local = local[:i]
			break
		}
	}
	// Sanitize to [a-z0-9_]; lowercase. Truncate to 32 chars (username max).
	out := make([]byte, 0, len(local))
	for _, c := range []byte(local) {
		switch {
		case c >= 'a' && c <= 'z':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+32) // lowercase
		case c >= '0' && c <= '9', c == '_':
			out = append(out, c)
		default:
			if len(out) > 0 && out[len(out)-1] != '_' {
				out = append(out, '_')
			}
		}
		if len(out) >= 32 {
			break
		}
	}
	out = trimUnderscores(out)
	if len(out) < 3 {
		// Too short; the UserRepo will reject and surface InvalidArgument.
		return local
	}
	// Ensure starts with a letter; prefix "u" if not.
	first := out[0]
	if first < 'a' || first > 'z' {
		out = append([]byte{'u'}, out...)
	}
	return string(out)
}

func trimUnderscores(b []byte) []byte {
	for len(b) > 0 && b[0] == '_' {
		b = b[1:]
	}
	for len(b) > 0 && b[len(b)-1] == '_' {
		b = b[:len(b)-1]
	}
	return b
}
