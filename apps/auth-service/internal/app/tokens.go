package app

import (
	"context"

	"vibesync/apps/auth-service/internal/domain"
	"vibesync/apps/auth-service/internal/ports"
)

// issueAccessToken signs a fresh access token for the given user. The TTL comes
// from config; claims carry the user id, system role, issuer, and a fresh jti.
//
// The scope is empty in Phase 4 (we don't model per-token scopes yet); the
// field is on the wire for future scopes without a contract change.
func (s *Service) issueAccessToken(ctx context.Context, u domain.User) (string, error) {
	now := s.now()
	expiresAt := now.Add(s.cfg.Auth.AccessTokenTTL)
	tok, err := s.signer.Sign(ctx, ports.AccessTokenClaims{
		Subject:    u.ID,
		SystemRole: int32(u.SystemRole.Number()),
		Issuer:     s.cfg.Auth.Issuer,
		Audience:   []string{"vibesync"}, // services allowed to consume this token
		IssuedAt:   now,
		ExpiresAt:  expiresAt,
		Scope:      "",
	})
	if err != nil {
		return "", err
	}
	return tok, nil
}
