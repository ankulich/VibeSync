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

// SessionRepo is the Postgres-backed SessionRepo.
type SessionRepo struct{}

// NewSessionRepo returns a SessionRepo.
func NewSessionRepo() *SessionRepo { return &SessionRepo{} }

// Create inserts a new session.
func (SessionRepo) Create(ctx context.Context, tx pgx.Tx, s domain.Session) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth.sessions
		    (id, user_id, device_label, family_id, created_at, last_seen_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		s.ID, s.UserID, s.DeviceLabel, s.FamilyID, s.CreatedAt, s.LastSeenAt, s.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("session_repo: create: %w", err)
	}
	return nil
}

// GetByID fetches a session by primary key.
func (SessionRepo) GetByID(ctx context.Context, tx pgx.Tx, id string) (domain.Session, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, user_id, device_label, family_id, created_at, last_seen_at, expires_at, revoked_at
		  FROM auth.sessions
		 WHERE id = $1`, id)
	var s domain.Session
	var revoked *string // NULL when not revoked
	err := row.Scan(&s.ID, &s.UserID, &s.DeviceLabel, &s.FamilyID,
		&s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt, &revoked)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Session{}, ports.NotFound("session", id)
		}
		return domain.Session{}, fmt.Errorf("session_repo: scan: %w", err)
	}
	return s, nil
}

// UpdateLastSeen records activity on the session.
func (SessionRepo) UpdateLastSeen(ctx context.Context, tx pgx.Tx, id string, at time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE auth.sessions SET last_seen_at = $2 WHERE id = $1`, id, at)
	if err != nil {
		return fmt.Errorf("session_repo: update_last_seen: %w", err)
	}
	return nil
}

// Revoke marks a session revoked (sets revoked_at). The refresh-token family
// is not auto-revoked here; the use case issues MarkRevoked on the active
// token explicitly so the family state is consistent.
func (SessionRepo) Revoke(ctx context.Context, tx pgx.Tx, id string, at time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE auth.sessions SET revoked_at = $2 WHERE id = $1`, id, at)
	if err != nil {
		return fmt.Errorf("session_repo: revoke: %w", err)
	}
	return nil
}

var _ ports.SessionRepo = (*SessionRepo)(nil)
