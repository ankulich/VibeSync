package app

import (
	"context"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	"vibesync/apps/auth-service/internal/domain"
	authv1 "vibesync/gen/go/vibesync/auth/v1"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	vberr "vibesync/libs/errors"
)

// Login authenticates a user by email + password and issues a token pair.
//
// Failure modes mapped to client-visible codes:
//   - email not found OR password mismatch → Unauthenticated("INVALID_CREDENTIALS")
//     (deliberately identical: don't leak which)
//   - user not in Active status            → PermissionDenied("USER_NOT_ACTIVE")
//   - DB errors                            → Internal(...) (logged, not leaked)
func (s *Service) Login(ctx context.Context, req *connect.Request[authv1.LoginRequest]) (*connect.Response[authv1.LoginResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	email := req.Msg.GetEmail()
	password := req.Msg.GetPassword()
	if email == "" || password == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.auth", "MISSING_CREDENTIALS", "email and password required")
	}

	session, tokens, err := s.loginUseCase(ctx, email, password, req.Msg.GetDeviceLabel())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&authv1.LoginResponse{
		Session: &authv1.AuthSession{
			Tokens: &authv1.TokenPair{
				AccessToken:  tokens.AccessToken,
				RefreshToken: tokens.RefreshToken,
				TokenType:    "Bearer",
				ExpiresIn:    tokens.ExpiresIn,
			},
			UserId: &commonv1.Id{Value: session.UserID},
		},
	}), nil
}

// tokenMaterial is the one-time-presented pair returned from a session creation
// or rotation. accessToken is the JWT; refreshToken is "<selector>.<validator>"
// in plaintext (the validator half is never persisted).
type tokenMaterial struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    uint64 // seconds
}

// loginUseCase orchestrates: load user → verify password → create session +
// root refresh token. All writes go in one tx so a crash mid-login leaves no
// orphan session.
func (s *Service) loginUseCase(ctx context.Context, email, password, deviceLabel string) (domain.Session, tokenMaterial, error) {
	var (
		session  domain.Session
		material tokenMaterial
	)
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		user, err := s.users.GetByEmail(ctx, tx, email)
		if err != nil {
			// Map not-found to INVALID_CREDENTIALS so the error code matches
			// the "wrong password" path. Other errors propagate as Internal.
			if isNotFound(err) {
				return vberr.Unauthenticated("INVALID_CREDENTIALS")
			}
			return err
		}
		if !user.HasPassword() {
			// OAuth-only user trying password login. Same code as a bad
			// password — don't leak "this email exists but is OAuth-only".
			return vberr.Unauthenticated("INVALID_CREDENTIALS")
		}
		if !s.hasher.Compare(user.PasswordHash, password) {
			return vberr.Unauthenticated("INVALID_CREDENTIALS")
		}
		if !user.CanAuthenticate() {
			return vberr.PermissionDenied("login", "user:"+user.ID)
		}

		// Build the session + family + root refresh token.
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

		// Issue the access token. No DB write for the access token (it's a
		// stateless JWT); revocation is via short TTL + refresh-chain burn.
		access, err := s.issueAccessToken(ctx, user)
		if err != nil {
			return err
		}

		session = sess
		material = tokenMaterial{
			AccessToken:  access,
			RefreshToken: validator.PresentationToken(),
			ExpiresIn:    uint64(s.cfg.Auth.AccessTokenTTL.Seconds()),
		}
		return nil
	})
	return session, material, err
}
