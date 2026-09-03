package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/room-service/internal/domain"
)

// PermissionRepo implements ports.PermissionRepo.
type PermissionRepo struct{}

// NewPermissionRepo constructs a PermissionRepo.
func NewPermissionRepo() *PermissionRepo { return &PermissionRepo{} }

// Set replaces a member's permission bitmask (0 revokes everything).
func (PermissionRepo) Set(ctx context.Context, tx pgx.Tx, roomID, userID string, perms domain.Permissions) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO room_permissions (room_id, user_id, permissions, updated_at)
		VALUES ($1,$2,$3,now())
		ON CONFLICT (room_id, user_id) DO UPDATE SET
		    permissions = EXCLUDED.permissions, updated_at = now()`,
		roomID, userID, int16(perms))
	if err != nil {
		return fmt.Errorf("permission_repo: set: %w", err)
	}
	return nil
}

// Get returns a member's permission bitmask; 0 when nothing was granted.
func (PermissionRepo) Get(ctx context.Context, tx pgx.Tx, roomID, userID string) (domain.Permissions, error) {
	var perms int16
	err := tx.QueryRow(ctx, `
		SELECT permissions FROM room_permissions WHERE room_id = $1 AND user_id = $2`,
		roomID, userID).Scan(&perms)
	if err != nil {
		if isNotFoundErr(err) {
			return 0, nil // no row = no grants, not an error
		}
		return 0, fmt.Errorf("permission_repo: get: %w", err)
	}
	return domain.Permissions(perms), nil
}

// ListByRoom returns all granted bitmasks in a room, keyed by user id.
func (PermissionRepo) ListByRoom(ctx context.Context, tx pgx.Tx, roomID string) (map[string]domain.Permissions, error) {
	rows, err := tx.Query(ctx, `
		SELECT user_id, permissions FROM room_permissions WHERE room_id = $1`, roomID)
	if err != nil {
		return nil, fmt.Errorf("permission_repo: list: %w", err)
	}
	defer rows.Close()
	out := make(map[string]domain.Permissions)
	for rows.Next() {
		var userID string
		var perms int16
		if err := rows.Scan(&userID, &perms); err != nil {
			return nil, fmt.Errorf("permission_repo: list scan: %w", err)
		}
		out[userID] = domain.Permissions(perms)
	}
	return out, rows.Err()
}

// Delete removes a member's grants (e.g. when they leave or are kicked).
func (PermissionRepo) Delete(ctx context.Context, tx pgx.Tx, roomID, userID string) error {
	_, err := tx.Exec(ctx, `
		DELETE FROM room_permissions WHERE room_id = $1 AND user_id = $2`, roomID, userID)
	if err != nil {
		return fmt.Errorf("permission_repo: delete: %w", err)
	}
	return nil
}

// isNotFoundErr reports a pgx no-rows error.
func isNotFoundErr(err error) bool {
	return err == pgx.ErrNoRows
}
