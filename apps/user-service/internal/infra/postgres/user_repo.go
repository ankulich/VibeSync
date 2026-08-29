package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/user-service/internal/domain"
	"vibesync/apps/user-service/internal/ports"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

// UserRepo is the Postgres-backed UserRepo implementation.
type UserRepo struct{}

// NewUserRepo returns a UserRepo. Stateless; tx is passed per call.
func NewUserRepo() *UserRepo { return &UserRepo{} }

const userColumns = `id, email, username, display_name, avatar_url, system_role, created_at, updated_at`

// Upsert inserts a new user or, on conflict (id), updates the identity fields
// from the event. This is the idempotent projection path: a redelivered event
// produces the same row. Display_name and avatar_url are NOT overwritten on
// conflict — the User Service may have updated them via UpdateUser since the
// original projection, and overwriting would lose user edits.
func (UserRepo) Upsert(ctx context.Context, tx pgx.Tx, u domain.User) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO users (id, email, username, display_name, avatar_url, system_role, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET
		    email       = EXCLUDED.email,
		    username    = EXCLUDED.username,
		    system_role = EXCLUDED.system_role,
		    updated_at  = EXCLUDED.updated_at`,
		u.ID, u.Email, u.Username, u.DisplayName, u.AvatarURL,
		int16(u.SystemRole), u.CreatedAt, u.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("user_repo: upsert: %w", err)
	}
	return nil
}

// GetByID loads a user by primary key.
func (UserRepo) GetByID(ctx context.Context, tx pgx.Tx, id string) (domain.User, error) {
	row := tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id)
	return scanUser(row, id)
}

// Update writes the mutable fields (display_name, avatar_url, updated_at).
// Identity fields (email, username, role) are NOT updated — they belong to Auth.
func (UserRepo) Update(ctx context.Context, tx pgx.Tx, u domain.User) error {
	tag, err := tx.Exec(ctx, `
		UPDATE users
		   SET display_name = $2, avatar_url = $3, updated_at = $4
		 WHERE id = $1`,
		u.ID, u.DisplayName, u.AvatarURL, u.UpdatedAt)
	if err != nil {
		return fmt.Errorf("user_repo: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ports.NotFound("user", u.ID)
	}
	return nil
}

// List returns a page of users ordered by created_at DESC. Keyset pagination:
// cursor is a ULID string; only users with created_at strictly before the
// cursor's embedded time are returned. search filters by username via
// trigram similarity (ILIKE %search%).
func (UserRepo) List(ctx context.Context, tx pgx.Tx, cursor string, limit int, search string) ([]domain.User, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// Build the query dynamically. The cursor is a ULID; we decode its embedded
	// timestamp to get the created_at boundary. An empty cursor means "first page".
	args := []any{}
	query := `SELECT ` + userColumns + ` FROM users`
	where := ""
	if search != "" {
		args = append(args, "%"+search+"%")
		where = fmt.Sprintf(" WHERE username ILIKE $%d", len(args))
	}
	if cursor != "" {
		args = append(args, cursor)
		if where == "" {
			where = fmt.Sprintf(" WHERE id < $%d", len(args))
		} else {
			where += fmt.Sprintf(" AND id < $%d", len(args))
		}
	}
	query += where + fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args)+1)
	args = append(args, limit+1) // fetch one extra to determine if there's a next page

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("user_repo: list: %w", err)
	}
	defer rows.Close()
	var users []domain.User
	for rows.Next() {
		u, err := scanUser(rows, "")
		if err != nil {
			return nil, "", err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("user_repo: list rows: %w", err)
	}

	// Determine next cursor: if we fetched limit+1 rows, there's more.
	nextCursor := ""
	if len(users) > limit {
		nextCursor = users[limit].ID // the (limit+1)th row's ID is the cursor
		users = users[:limit]        // trim to the requested page
	}
	return users, nextCursor, nil
}

// rowScanner abstracts *pgx.Row and *pgx.Rows for scanUser.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanUser maps one row to a domain.User.
func scanUser(row rowScanner, idForErr string) (domain.User, error) {
	var u domain.User
	var role int16
	err := row.Scan(&u.ID, &u.Email, &u.Username, &u.DisplayName, &u.AvatarURL,
		&role, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, ports.NotFound("user", idForErr)
		}
		return domain.User{}, fmt.Errorf("user_repo: scan: %w", err)
	}
	u.SystemRole = commonv1.SystemRole(role)
	return u, nil
}

var _ ports.UserRepo = (*UserRepo)(nil)
