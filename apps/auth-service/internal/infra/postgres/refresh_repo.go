package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/auth-service/internal/domain"
	"vibesync/apps/auth-service/internal/ports"
)

// RefreshRepo is the Postgres-backed RefreshTokenRepo. The reuse-detection
// methods (MarkUsed, RevokeFamily) are first-class because they drive the
// security-critical family revocation (ADR-0011).
type RefreshRepo struct{}

// NewRefreshRepo returns a RefreshRepo.
func NewRefreshRepo() *RefreshRepo { return &RefreshRepo{} }

// Create inserts a new refresh token row.
func (RefreshRepo) Create(ctx context.Context, tx pgx.Tx, t domain.RefreshToken) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth.refresh_tokens
		    (id, family_id, user_id, session_id, selector, validator_hash, rotated_from,
		     status, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		t.ID, t.FamilyID, t.UserID, t.SessionID, t.Selector, t.ValidatorHash,
		nullableStr(t.RotatedFrom), int16(t.Status), t.ExpiresAt, t.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("refresh_repo: create: %w", err)
	}
	return nil
}

// GetBySelector fetches a token by its selector (the lookup half of the
// selector/validator split). O(1) via the UNIQUE index.
func (RefreshRepo) GetBySelector(ctx context.Context, tx pgx.Tx, selector string) (domain.RefreshToken, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, family_id, user_id, session_id, selector, validator_hash, rotated_from,
		       status, expires_at, created_at, used_at, revoked_at
		  FROM auth.refresh_tokens
		 WHERE selector = $1`, selector)
	return scanRefreshToken(row)
}

// MarkUsed transitions a token to Used. Called inside the refresh tx after
// staging the new (rotated) token, so the old + new commit atomically.
func (RefreshRepo) MarkUsed(ctx context.Context, tx pgx.Tx, id string, at time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth.refresh_tokens
		   SET status = $3, used_at = $2
		 WHERE id = $1`, id, at, int16(domain.RefreshTokenStatusUsed))
	if err != nil {
		return fmt.Errorf("refresh_repo: mark_used: %w", err)
	}
	return nil
}

// MarkRevoked transitions a token to Revoked. Called by Logout.
func (RefreshRepo) MarkRevoked(ctx context.Context, tx pgx.Tx, id string, at time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth.refresh_tokens
		   SET status = $3, revoked_at = $2
		 WHERE id = $1`, id, at, int16(domain.RefreshTokenStatusRevoked))
	if err != nil {
		return fmt.Errorf("refresh_repo: mark_revoked: %w", err)
	}
	return nil
}

// RevokeFamily marks every token in the family Compromised. This is the
// reuse-detection response: presenting a USED token means the chain leaked,
// so the entire family is burned. Returns the count for metrics.
//
// Atomic with the rest of the refresh transaction so an attacker cannot race
// a fresh issue between detection and revocation.
func (RefreshRepo) RevokeFamily(ctx context.Context, tx pgx.Tx, familyID string, at time.Time) (int64, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE auth.refresh_tokens
		   SET status = $3, revoked_at = $2
		 WHERE family_id = $1
		   AND status != $3`, familyID, at, int16(domain.RefreshTokenStatusCompromised))
	if err != nil {
		return 0, fmt.Errorf("refresh_repo: revoke_family: %w", err)
	}
	return tag.RowsAffected(), nil
}

func scanRefreshToken(row rowScanner) (domain.RefreshToken, error) {
	var t domain.RefreshToken
	var status int16
	var rotatedFrom *string
	var usedAt, revokedAt *time.Time
	err := row.Scan(
		&t.ID, &t.FamilyID, &t.UserID, &t.SessionID, &t.Selector, &t.ValidatorHash,
		&rotatedFrom, &status, &t.ExpiresAt, &t.CreatedAt, &usedAt, &revokedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RefreshToken{}, ports.NotFound("refresh_token", t.Selector)
		}
		return domain.RefreshToken{}, fmt.Errorf("refresh_repo: scan: %w", err)
	}
	if rotatedFrom != nil {
		t.RotatedFrom = *rotatedFrom
	}
	if usedAt != nil {
		t.UsedAt = *usedAt
	}
	if revokedAt != nil {
		t.RevokedAt = *revokedAt
	}
	t.Status = domain.RefreshTokenStatus(status)
	return t, nil
}

// nullableStr returns nil for an empty string so the column stores NULL rather
// than "". Used for rotated_from, which is empty only on the family-root token.
func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

var _ ports.RefreshTokenRepo = (*RefreshRepo)(nil)
