package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/room-service/internal/domain"
)

// RoomRepo implements ports.RoomRepo.
type RoomRepo struct{}

// NewRoomRepo constructs a RoomRepo.
func NewRoomRepo() *RoomRepo { return &RoomRepo{} }

const roomColumns = `id, slug, name, description, visibility, owner_id, max_members, member_count, created_at, updated_at`

// Create inserts a new room row.
func (RoomRepo) Create(ctx context.Context, tx pgx.Tx, r domain.Room) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO rooms (id, slug, name, description, visibility, owner_id, max_members, member_count, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		r.ID, r.Slug, r.Name, r.Description, int16(r.Visibility), r.OwnerID,
		r.MaxMembers, r.MemberCount, r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return fmt.Errorf("room_repo: create: %w", err)
	}
	return nil
}

// GetByID loads a room by its ID.
func (RoomRepo) GetByID(ctx context.Context, tx pgx.Tx, id string) (domain.Room, error) {
	row := tx.QueryRow(ctx, `SELECT `+roomColumns+` FROM rooms WHERE id = $1`, id)
	return scanRoom(row, id)
}

// GetBySlug loads a room by its slug.
func (RoomRepo) GetBySlug(ctx context.Context, tx pgx.Tx, slug string) (domain.Room, error) {
	row := tx.QueryRow(ctx, `SELECT `+roomColumns+` FROM rooms WHERE slug = $1`, slug)
	return scanRoom(row, slug)
}

// Update writes the mutable fields of a room row.
func (RoomRepo) Update(ctx context.Context, tx pgx.Tx, r domain.Room) error {
	_, err := tx.Exec(ctx, `
		UPDATE rooms SET slug=$2, name=$3, description=$4, visibility=$5, max_members=$6, member_count=$7, updated_at=$8
		WHERE id = $1`,
		r.ID, r.Slug, r.Name, r.Description, int16(r.Visibility), r.MaxMembers, r.MemberCount, r.UpdatedAt)
	if err != nil {
		return fmt.Errorf("room_repo: update: %w", err)
	}
	return nil
}

// Delete removes a room row by ID.
func (RoomRepo) Delete(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `DELETE FROM rooms WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("room_repo: delete: %w", err)
	}
	return nil
}

// List returns a page of rooms filtered by an optional name search and a set
// of allowed visibilities, ordered by created_at descending. The cursor is the
// last ID of the previous page; the returned string is the next page cursor
// (empty when there are no more rows).
func (RoomRepo) List(ctx context.Context, tx pgx.Tx, cursor string, limit int, search string, visibilities []domain.RoomVisibility) ([]domain.Room, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var args []any
	var conds []string
	if search != "" {
		args = append(args, "%"+strings.ToLower(search)+"%")
		conds = append(conds, fmt.Sprintf("LOWER(name) ILIKE $%d", len(args)))
	}
	if len(visibilities) > 0 {
		var ph []string
		for _, v := range visibilities {
			args = append(args, int16(v))
			ph = append(ph, fmt.Sprintf("$%d", len(args)))
		}
		conds = append(conds, "visibility IN ("+strings.Join(ph, ",")+")")
	}
	if cursor != "" {
		args = append(args, cursor)
		conds = append(conds, fmt.Sprintf("id < $%d", len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	args = append(args, limit+1)
	query := `SELECT ` + roomColumns + ` FROM rooms` + where + fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("room_repo: list: %w", err)
	}
	defer rows.Close()
	var rooms []domain.Room
	for rows.Next() {
		r, err := scanRoom(rows, "")
		if err != nil {
			return nil, "", err
		}
		rooms = append(rooms, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("room_repo: list rows: %w", err)
	}
	nextCursor := ""
	if len(rooms) > limit {
		nextCursor = rooms[limit].ID
		rooms = rooms[:limit]
	}
	return rooms, nextCursor, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRoom(row rowScanner, idForErr string) (domain.Room, error) {
	var r domain.Room
	var vis int16
	err := row.Scan(&r.ID, &r.Slug, &r.Name, &r.Description, &vis, &r.OwnerID,
		&r.MaxMembers, &r.MemberCount, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return domain.Room{}, mapErr("room", idForErr, err)
	}
	r.Visibility = domain.RoomVisibility(vis)
	return r, nil
}
