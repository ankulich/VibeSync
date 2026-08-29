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

// OAuthFlowRepo persists transient OAuth flow state. Single-use: GetAndConsume
// deletes the row after reading so state cannot be replayed.
type OAuthFlowRepo struct{}

// NewOAuthFlowRepo returns an OAuthFlowRepo.
func NewOAuthFlowRepo() *OAuthFlowRepo { return &OAuthFlowRepo{} }

// Create stores a new flow state. The PK (state) uniqueness guarantees no
// collision even under concurrent BeginOAuth calls.
func (OAuthFlowRepo) Create(ctx context.Context, tx pgx.Tx, f domain.OAuthFlow) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth.oauth_flows
		    (state, provider, redirect_uri, code_challenge, user_agent, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		f.State, f.Provider, f.RedirectURI, f.CodeChallenge, f.UserAgent,
		f.CreatedAt, f.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("oauthflow_repo: create: %w", err)
	}
	return nil
}

// GetAndConsume reads a flow by state and deletes it in the same tx. Returns
// ports.NotFound if the state doesn't exist (expired-and-swept, or never
// existed). Single-use semantics: a second call with the same state fails.
func (OAuthFlowRepo) GetAndConsume(ctx context.Context, tx pgx.Tx, state string) (domain.OAuthFlow, error) {
	row := tx.QueryRow(ctx, `
		SELECT state, provider, redirect_uri, code_challenge, user_agent, created_at, expires_at
		  FROM auth.oauth_flows
		 WHERE state = $1`, state)
	var f domain.OAuthFlow
	err := row.Scan(&f.State, &f.Provider, &f.RedirectURI, &f.CodeChallenge,
		&f.UserAgent, &f.CreatedAt, &f.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.OAuthFlow{}, ports.NotFound("oauth_flow", state)
		}
		return domain.OAuthFlow{}, fmt.Errorf("oauthflow_repo: scan: %w", err)
	}
	// Consume: delete in the same tx so a concurrent CompleteOAuth cannot
	// race the read.
	if _, err := tx.Exec(ctx, `DELETE FROM auth.oauth_flows WHERE state = $1`, state); err != nil {
		return domain.OAuthFlow{}, fmt.Errorf("oauthflow_repo: consume: %w", err)
	}
	return f, nil
}

// Delete removes a flow state. Idempotent.
func (OAuthFlowRepo) Delete(ctx context.Context, tx pgx.Tx, state string) error {
	_, err := tx.Exec(ctx, `DELETE FROM auth.oauth_flows WHERE state = $1`, state)
	if err != nil {
		return fmt.Errorf("oauthflow_repo: delete: %w", err)
	}
	return nil
}

// DeleteExpired sweeps rows past their expiry. Returns the count for metrics.
// Called by a background ticker in main.go.
func (OAuthFlowRepo) DeleteExpired(ctx context.Context, tx pgx.Tx, before time.Time) (int64, error) {
	tag, err := tx.Exec(ctx, `DELETE FROM auth.oauth_flows WHERE expires_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("oauthflow_repo: delete_expired: %w", err)
	}
	return tag.RowsAffected(), nil
}

var _ ports.OAuthFlowRepo = (*OAuthFlowRepo)(nil)
