package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/auth-service/internal/domain"
	"vibesync/apps/auth-service/internal/ports"
)

// OAuthAccountRepo persists links between VibeSync users and provider
// identities. (provider, provider_user_id) is the PK; (user_id, provider) is
// unique so a user has at most one link per provider.
type OAuthAccountRepo struct{}

// NewOAuthAccountRepo returns an OAuthAccountRepo.
func NewOAuthAccountRepo() *OAuthAccountRepo { return &OAuthAccountRepo{} }

// Upsert inserts or updates a provider link. On conflict (same provider +
// provider_user_id), the user_id is NOT changed — re-linking to a different
// user requires explicit unlinking first. This prevents silent account
// takeover if a provider reuses an id.
func (OAuthAccountRepo) Upsert(ctx context.Context, tx pgx.Tx, a domain.OAuthAccount) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth.oauth_accounts
		    (provider, provider_user_id, user_id, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (provider, provider_user_id) DO NOTHING`,
		a.Provider, a.ProviderUserID, a.UserID, a.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("oauthaccount_repo: upsert: %w", err)
	}
	return nil
}

// GetByProvider looks up a link by provider + provider_user_id. Used on OAuth
// login to find an existing user for an inbound provider identity.
func (OAuthAccountRepo) GetByProvider(ctx context.Context, tx pgx.Tx, provider, providerUserID string) (domain.OAuthAccount, error) {
	row := tx.QueryRow(ctx, `
		SELECT provider, provider_user_id, user_id, created_at
		  FROM auth.oauth_accounts
		 WHERE provider = $1 AND provider_user_id = $2`, provider, providerUserID)
	return scanOAuthAccount(row, provider, providerUserID)
}

// GetByUser looks up a user's link for a specific provider. Used to display
// "linked accounts" and to gate login (must have a link OR a password).
func (OAuthAccountRepo) GetByUser(ctx context.Context, tx pgx.Tx, userID, provider string) (domain.OAuthAccount, error) {
	row := tx.QueryRow(ctx, `
		SELECT provider, provider_user_id, user_id, created_at
		  FROM auth.oauth_accounts
		 WHERE user_id = $1 AND provider = $2`, userID, provider)
	return scanOAuthAccount(row, provider, userID)
}

// ListByUser returns ALL provider links for a user. Used by the profile
// page's ListLinkedProviders RPC.
func (OAuthAccountRepo) ListByUser(ctx context.Context, tx pgx.Tx, userID string) ([]domain.OAuthAccount, error) {
	rows, err := tx.Query(ctx, `
		SELECT provider, provider_user_id, user_id, created_at
		  FROM auth.oauth_accounts
		 WHERE user_id = $1
		 ORDER BY created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("oauthaccount_repo: list_by_user: %w", err)
	}
	defer rows.Close()
	var out []domain.OAuthAccount
	for rows.Next() {
		var a domain.OAuthAccount
		if err := rows.Scan(&a.Provider, &a.ProviderUserID, &a.UserID, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("oauthaccount_repo: list_by_user scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanOAuthAccount(row rowScanner, entity, id string) (domain.OAuthAccount, error) {
	var a domain.OAuthAccount
	err := row.Scan(&a.Provider, &a.ProviderUserID, &a.UserID, &a.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.OAuthAccount{}, ports.NotFound(entity, id)
		}
		return domain.OAuthAccount{}, fmt.Errorf("oauthaccount_repo: scan: %w", err)
	}
	return a, nil
}

var _ ports.OAuthAccountRepo = (*OAuthAccountRepo)(nil)
