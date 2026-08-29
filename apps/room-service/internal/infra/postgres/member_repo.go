package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/room-service/internal/domain"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

// MemberRepo implements ports.MemberRepo.
type MemberRepo struct{}

// NewMemberRepo constructs a MemberRepo.
func NewMemberRepo() *MemberRepo { return &MemberRepo{} }

// Upsert inserts a member, or updates their role on conflict.
func (MemberRepo) Upsert(ctx context.Context, tx pgx.Tx, m domain.Member) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO room_members (room_id, user_id, role, joined_at)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (room_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		m.RoomID, m.UserID, int16(m.Role), m.JoinedAt)
	if err != nil {
		return fmt.Errorf("member_repo: upsert: %w", err)
	}
	return nil
}

// Get loads a single member by room and user ID.
func (MemberRepo) Get(ctx context.Context, tx pgx.Tx, roomID, userID string) (domain.Member, error) {
	row := tx.QueryRow(ctx, `SELECT room_id, user_id, role, joined_at FROM room_members WHERE room_id=$1 AND user_id=$2`, roomID, userID)
	var m domain.Member
	var role int16
	err := row.Scan(&m.RoomID, &m.UserID, &role, &m.JoinedAt)
	if err != nil {
		return domain.Member{}, mapErr("member", userID, err)
	}
	m.Role = commonv1.RoomRole(role)
	return m, nil
}

// List returns all members of a room, ordered by join time.
func (MemberRepo) List(ctx context.Context, tx pgx.Tx, roomID string) ([]domain.Member, error) {
	rows, err := tx.Query(ctx, `SELECT room_id, user_id, role, joined_at FROM room_members WHERE room_id=$1 ORDER BY joined_at`, roomID)
	if err != nil {
		return nil, fmt.Errorf("member_repo: list: %w", err)
	}
	defer rows.Close()
	var members []domain.Member
	for rows.Next() {
		var m domain.Member
		var role int16
		if err := rows.Scan(&m.RoomID, &m.UserID, &role, &m.JoinedAt); err != nil {
			return nil, fmt.Errorf("member_repo: scan: %w", err)
		}
		m.Role = commonv1.RoomRole(role)
		members = append(members, m)
	}
	return members, rows.Err()
}

// Delete removes a member from a room.
func (MemberRepo) Delete(ctx context.Context, tx pgx.Tx, roomID, userID string) error {
	_, err := tx.Exec(ctx, `DELETE FROM room_members WHERE room_id=$1 AND user_id=$2`, roomID, userID)
	if err != nil {
		return fmt.Errorf("member_repo: delete: %w", err)
	}
	return nil
}

// UpdateRole sets a member's room role.
func (MemberRepo) UpdateRole(ctx context.Context, tx pgx.Tx, roomID, userID string, role commonv1.RoomRole) error {
	_, err := tx.Exec(ctx, `UPDATE room_members SET role=$3 WHERE room_id=$1 AND user_id=$2`, roomID, userID, int16(role))
	if err != nil {
		return fmt.Errorf("member_repo: update_role: %w", err)
	}
	return nil
}

// IncrementRoomMemberCount adjusts a room's member_count by delta (positive
// or negative), keeping the denormalized count in sync with room_members.
func (MemberRepo) IncrementRoomMemberCount(ctx context.Context, tx pgx.Tx, roomID string, delta int) error {
	_, err := tx.Exec(ctx, `UPDATE rooms SET member_count = member_count + $2 WHERE id = $1`, roomID, delta)
	if err != nil {
		return fmt.Errorf("member_repo: increment_count: %w", err)
	}
	return nil
}
