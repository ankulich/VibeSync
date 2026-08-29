package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/auth-service/internal/ports"
)

// ProviderToken is the stored (encrypted) provider token pair for a user.
// The encryption/decryption happens in the app layer (which holds the
// KeyCipher); the repo only sees opaque bytes.
type ProviderToken struct {
	Provider        string
	UserID          string
	AccessTokenEnc  []byte
	RefreshTokenEnc []byte
	ExpiresAt       time.Time
	UpdatedAt       time.Time
}

// ProviderTokenRepo persists encrypted provider tokens.
type ProviderTokenRepo struct{}

// NewProviderTokenRepo returns a ProviderTokenRepo.
func NewProviderTokenRepo() *ProviderTokenRepo { return &ProviderTokenRepo{} }

// Upsert stores or updates the token pair for (provider, user_id).
func (ProviderTokenRepo) Upsert(ctx context.Context, tx pgx.Tx, t ProviderToken) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth.provider_tokens
		    (provider, user_id, access_token_encrypted, refresh_token_encrypted, expires_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (provider, user_id) DO UPDATE SET
		    access_token_encrypted  = EXCLUDED.access_token_encrypted,
		    refresh_token_encrypted = EXCLUDED.refresh_token_encrypted,
		    expires_at              = EXCLUDED.expires_at,
		    updated_at              = EXCLUDED.updated_at`,
		t.Provider, t.UserID, t.AccessTokenEnc, t.RefreshTokenEnc, t.ExpiresAt, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("provider_token_repo: upsert: %w", err)
	}
	return nil
}

// Get loads the stored token pair for (provider, user_id).
func (ProviderTokenRepo) Get(ctx context.Context, tx pgx.Tx, provider, userID string) (ProviderToken, error) {
	row := tx.QueryRow(ctx, `
		SELECT provider, user_id, access_token_encrypted, refresh_token_encrypted, expires_at, updated_at
		  FROM auth.provider_tokens
		 WHERE provider = $1 AND user_id = $2`, provider, userID)
	var t ProviderToken
	err := row.Scan(&t.Provider, &t.UserID, &t.AccessTokenEnc, &t.RefreshTokenEnc, &t.ExpiresAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProviderToken{}, ports.NotFound("provider_token", provider+":"+userID)
		}
		return ProviderToken{}, fmt.Errorf("provider_token_repo: get: %w", err)
	}
	return t, nil
}
