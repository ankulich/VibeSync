package app

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	"vibesync/apps/auth-service/internal/domain"
	authv1 "vibesync/gen/go/vibesync/auth/v1"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	vberr "vibesync/libs/errors"
)

// RefreshToken rotates a refresh token. The presented token is single-use:
// presenting it again after a successful rotation is treated as reuse and
// revokes the entire family (ADR-0011).
//
// Failure modes:
//   - malformed token (no "." separator) → InvalidArgument
//   - selector not found                  → Unauthenticated("INVALID_REFRESH_TOKEN")
//   - validator mismatch                  → Unauthenticated("INVALID_REFRESH_TOKEN")
//   - token USED                          → REUSE: revoke family, Unauthenticated("REUSE_DETECTED")
//   - token EXPIRED / REVOKED / COMPROMISED → Unauthenticated with specific reason
//   - user no longer active               → PermissionDenied
func (s *Service) RefreshToken(ctx context.Context, req *connect.Request[authv1.RefreshTokenRequest]) (*connect.Response[authv1.RefreshTokenResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	presented := req.Msg.GetRefreshToken()
	if presented == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.auth", "MISSING_REFRESH_TOKEN", "refresh_token required")
	}

	session, tokens, err := s.refreshUseCase(ctx, presented)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv1.RefreshTokenResponse{
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

// Logout revokes a refresh token. Idempotent: revoking an already-revoked or
// unknown token returns revoked=true without error.
func (s *Service) Logout(ctx context.Context, req *connect.Request[authv1.LogoutRequest]) (*connect.Response[authv1.LogoutResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	presented := req.Msg.GetRefreshToken()
	if presented == "" {
		return connect.NewResponse(&authv1.LogoutResponse{Revoked: false}), nil
	}
	selector, _ := splitSelectorValidator(presented)
	if selector == "" {
		return connect.NewResponse(&authv1.LogoutResponse{Revoked: false}), nil
	}

	revoked := false
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rt, err := s.refresh.GetBySelector(ctx, tx, selector)
		if err != nil {
			if isNotFound(err) {
				return nil // unknown token: idempotent no-op
			}
			return err
		}
		if rt.Status == domain.RefreshTokenStatusActive {
			if err := s.refresh.MarkRevoked(ctx, tx, rt.ID, s.now()); err != nil {
				return err
			}
			revoked = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv1.LogoutResponse{Revoked: revoked}), nil
}

// refreshUseCase is the rotation + reuse-detection logic. All branches that
// mutate state do so inside the tx so the rotation (or family revocation) is
// atomic with the lookup.
func (s *Service) refreshUseCase(ctx context.Context, presented string) (domain.Session, tokenMaterial, error) {
	var (
		session  domain.Session
		material tokenMaterial
	)
	selector, validator := splitSelectorValidator(presented)
	if selector == "" || validator == "" {
		return domain.Session{}, tokenMaterial{}, vberr.InvalidArgumentFor("vibesync.auth", "MALFORMED_REFRESH_TOKEN", "expected <selector>.<validator>")
	}

	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rt, err := s.refresh.GetBySelector(ctx, tx, selector)
		if err != nil {
			if isNotFound(err) {
				return vberr.Unauthenticated("INVALID_REFRESH_TOKEN")
			}
			return err
		}

		// Verify the validator half BEFORE consulting status. A wrong validator
		// is treated the same as "no such token" — never as reuse, because an
		// attacker with a guessed selector but no validator shouldn't trigger
		// family revocation (a denial-of-service vector).
		if !rt.VerifyValidator(validator, s.hasher.Compare) {
			return vberr.Unauthenticated("INVALID_REFRESH_TOKEN")
		}

		// Classify the use: this is the security-critical decision point.
		now := s.now()
		action := rt.ClassifyUse(now)
		switch action.Outcome {
		case domain.UseOutcomeRotate:
			// Happy path: rotate.
		case domain.UseOutcomeReuse:
			// Reuse of a USED token → revoke the entire family.
			if _, ferr := s.refresh.RevokeFamily(ctx, tx, rt.FamilyID, now); ferr != nil {
				return ferr
			}
			// The reuse itself must be reported as Unauthenticated so the
			// (legitimate) client re-authenticates. The action.Err is the
			// typed ErrReuse; wrap it for the client.
			return vberr.Unauthenticated("REUSE_DETECTED").
				WithMeta("family_id", rt.FamilyID).
				WithCause(action.Err)
		case domain.UseOutcomeCompromised:
			return vberr.Unauthenticated("FAMILY_COMPROMISED").WithCause(action.Err)
		case domain.UseOutcomeRevoked:
			return vberr.Unauthenticated("REVOKED").WithCause(action.Err)
		case domain.UseOutcomeExpired:
			return vberr.Unauthenticated("EXPIRED").WithCause(action.Err)
		default:
			return vberr.Internal("UNKNOWN_TOKEN_STATE", "unhandled refresh token state")
		}

		// Load the user so we can issue a fresh access token and refuse if the
		// account is no longer active.
		user, err := s.users.GetByID(ctx, tx, rt.UserID)
		if err != nil {
			if isNotFound(err) {
				return vberr.Unauthenticated("INVALID_REFRESH_TOKEN")
			}
			return err
		}
		if !user.CanAuthenticate() {
			return vberr.PermissionDenied("refresh", "user:"+user.ID)
		}

		// Mark the presented token used; stage the new (rotated) token in the
		// same family. Both commit atomically.
		if err := s.refresh.MarkUsed(ctx, tx, rt.ID, now); err != nil {
			return err
		}
		newID := s.idgen.New()
		newRT, newValidator, err := domain.NewRefreshToken(
			now, newID, rt.FamilyID, user.ID, rt.SessionID, rt.ID,
			s.cfg.Auth.RefreshTokenTTL, s.hasher.Hash,
		)
		if err != nil {
			return err
		}
		if err := s.refresh.Create(ctx, tx, newRT); err != nil {
			return err
		}

		// Refresh the session's LastSeen so the active-sessions list reflects
		// real activity.
		if err := s.sessions.UpdateLastSeen(ctx, tx, rt.SessionID, now); err != nil {
			return err
		}

		// Issue a fresh access token.
		access, err := s.issueAccessToken(ctx, user)
		if err != nil {
			return err
		}

		// Echo the session for the response.
		sess, err := s.sessions.GetByID(ctx, tx, rt.SessionID)
		if err != nil {
			return err
		}
		session = sess
		material = tokenMaterial{
			AccessToken:  access,
			RefreshToken: newValidator.PresentationToken(),
			ExpiresIn:    uint64(s.cfg.Auth.AccessTokenTTL.Seconds()),
		}
		return nil
	})
	if err != nil {
		return domain.Session{}, tokenMaterial{}, err
	}
	return session, material, nil
}

// splitSelectorValidator parses "<selector>.<validator>" into its halves.
// Returns ("", "") for malformed input. The validator may itself contain '.'
// (base64url doesn't emit one, but be defensive).
func splitSelectorValidator(token string) (selector, validator string) {
	idx := strings.IndexByte(token, '.')
	if idx <= 0 || idx == len(token)-1 {
		return "", ""
	}
	return token[:idx], token[idx+1:]
}
