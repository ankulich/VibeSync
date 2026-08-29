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

// SigningKeyRepo is the Postgres-backed SigningKeyRepo. Stores the encrypted
// private key + canonical public JWK. Exactly one row is active at a time
// (enforced by the partial unique index signing_keys_one_active_idx).
type SigningKeyRepo struct{}

// NewSigningKeyRepo returns a SigningKeyRepo.
func NewSigningKeyRepo() *SigningKeyRepo { return &SigningKeyRepo{} }

// Upsert inserts or updates a signing key by KID.
func (SigningKeyRepo) Upsert(ctx context.Context, tx pgx.Tx, k domain.SigningKey) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth.signing_keys
		    (kid, status, private_encrypted, public_jwk, created_at, retired_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (kid) DO UPDATE SET
		    status = EXCLUDED.status,
		    private_encrypted = EXCLUDED.private_encrypted,
		    public_jwk = EXCLUDED.public_jwk,
		    retired_at = EXCLUDED.retired_at`,
		k.KID, int16(k.Status), k.PrivateEncrypted, k.PublicJWK, k.CreatedAt,
		nullableTime(k.RetiredAt),
	)
	if err != nil {
		return fmt.Errorf("signingkey_repo: upsert: %w", err)
	}
	return nil
}

// GetActive returns the single active signing key. Returns ports.NotFound
// when none exists (the use case bootstraps one on first startup).
func (SigningKeyRepo) GetActive(ctx context.Context, tx pgx.Tx) (domain.SigningKey, error) {
	row := tx.QueryRow(ctx, `
		SELECT kid, status, private_encrypted, public_jwk, created_at, retired_at
		  FROM auth.signing_keys
		 WHERE status = $1
		 LIMIT 1`, int16(domain.SigningKeyStatusActive))
	return scanSigningKey(row)
}

// ListVerifiable returns active + retired keys. The JWT verifier needs all of
// them: active for new tokens, retired for tokens issued before rotation.
func (SigningKeyRepo) ListVerifiable(ctx context.Context, tx pgx.Tx) ([]domain.SigningKey, error) {
	rows, err := tx.Query(ctx, `
		SELECT kid, status, private_encrypted, public_jwk, created_at, retired_at
		  FROM auth.signing_keys
		 WHERE status IN ($1, $2)
		 ORDER BY created_at DESC`,
		int16(domain.SigningKeyStatusActive), int16(domain.SigningKeyStatusRetired))
	if err != nil {
		return nil, fmt.Errorf("signingkey_repo: list: %w", err)
	}
	defer rows.Close()
	var out []domain.SigningKey
	for rows.Next() {
		k, err := scanSigningKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("signingkey_repo: list rows: %w", err)
	}
	return out, nil
}

// MarkRetired transitions a key to Retired. Called by Rotate after staging the
// new active key in the same transaction.
func (SigningKeyRepo) MarkRetired(ctx context.Context, tx pgx.Tx, kid string, at time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth.signing_keys
		   SET status = $3, retired_at = $2
		 WHERE kid = $1`, kid, at, int16(domain.SigningKeyStatusRetired))
	if err != nil {
		return fmt.Errorf("signingkey_repo: mark_retired: %w", err)
	}
	return nil
}

func scanSigningKey(row rowScanner) (domain.SigningKey, error) {
	var k domain.SigningKey
	var status int16
	var retiredAt *time.Time
	err := row.Scan(&k.KID, &status, &k.PrivateEncrypted, &k.PublicJWK, &k.CreatedAt, &retiredAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.SigningKey{}, ports.NotFound("signing_key", k.KID)
		}
		return domain.SigningKey{}, fmt.Errorf("signingkey_repo: scan: %w", err)
	}
	if retiredAt != nil {
		k.RetiredAt = *retiredAt
	}
	k.Status = domain.SigningKeyStatus(status)
	return k, nil
}

// nullableTime returns nil for the zero time so the column stores NULL.
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

var _ ports.SigningKeyRepo = (*SigningKeyRepo)(nil)
