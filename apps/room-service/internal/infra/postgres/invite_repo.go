package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// InviteRepo implements ports.InviteRepo.
type InviteRepo struct{}

// NewInviteRepo constructs an InviteRepo.
func NewInviteRepo() *InviteRepo { return &InviteRepo{} }

// Create inserts a new invite code for the given room.
func (InviteRepo) Create(ctx context.Context, tx pgx.Tx, code, roomID string, expiresAt *time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO room_invite (code, room_id, expires_at) VALUES ($1,$2,$3)`, code, roomID, expiresAt)
	if err != nil {
		return fmt.Errorf("invite_repo: create: %w", err)
	}
	return nil
}

// Get resolves an invite code to its room ID, rejecting expired codes.
func (InviteRepo) Get(ctx context.Context, tx pgx.Tx, code string) (string, error) {
	var roomID string
	err := tx.QueryRow(ctx, `SELECT room_id FROM room_invite WHERE code=$1 AND (expires_at IS NULL OR expires_at > now())`, code).Scan(&roomID)
	if err != nil {
		return "", mapErr("invite_code", code, err)
	}
	return roomID, nil
}

// Delete removes an invite code.
func (InviteRepo) Delete(ctx context.Context, tx pgx.Tx, code string) error {
	_, err := tx.Exec(ctx, `DELETE FROM room_invite WHERE code=$1`, code)
	if err != nil {
		return fmt.Errorf("invite_repo: delete: %w", err)
	}
	return nil
}
