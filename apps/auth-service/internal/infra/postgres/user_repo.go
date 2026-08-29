package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/auth-service/internal/domain"
	"vibesync/apps/auth-service/internal/ports"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

// UserRepo is the Postgres-backed UserRepo port implementation.
type UserRepo struct{}

// NewUserRepo returns a UserRepo. Stateless; the pool/tx is passed per call.
func NewUserRepo() *UserRepo { return &UserRepo{} }

const userColumns = `id, email, username, display_name, avatar_url, password_hash, system_role, status, created_at, updated_at`

// Create inserts a new user. Returns ports.ErrNotFound-shaped error on
// unique-violation mapped to AlreadyExists by the use case.
func (UserRepo) Create(ctx context.Context, tx pgx.Tx, u domain.User) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth.users
		    (id, email, username, display_name, avatar_url, password_hash, system_role, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		u.ID, u.Email, u.Username, u.DisplayName, u.AvatarURL, u.PasswordHash,
		int16(u.SystemRole.Number()), int16(u.Status), u.CreatedAt, u.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("user_repo: create: %w", err)
	}
	return nil
}

// GetByID fetches a user by primary key.
func (UserRepo) GetByID(ctx context.Context, tx pgx.Tx, id string) (domain.User, error) {
	row := tx.QueryRow(ctx, `SELECT `+userColumns+` FROM auth.users WHERE id = $1`, id)
	return scanUser(row)
}

// GetByEmail fetches a user by canonical (lowercased) email.
func (UserRepo) GetByEmail(ctx context.Context, tx pgx.Tx, email string) (domain.User, error) {
	row := tx.QueryRow(ctx, `SELECT `+userColumns+` FROM auth.users WHERE email = $1`, email)
	return scanUser(row)
}

// Update writes the mutable fields of a user. ID is the key.
func (UserRepo) Update(ctx context.Context, tx pgx.Tx, u domain.User) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth.users
		   SET display_name = $2, avatar_url = $3, password_hash = $4,
		       system_role = $5, status = $6, updated_at = $7
		 WHERE id = $1`,
		u.ID, u.DisplayName, u.AvatarURL, u.PasswordHash,
		int16(u.SystemRole.Number()), int16(u.Status), u.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("user_repo: update: %w", err)
	}
	return nil
}

// rowScanner abstracts *pgx.Row and *pgx.Rows for scanUser. Both implement
// Scan(dest ...any) error.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanUser maps one row to a domain.User, translating pgx.ErrNoRows to
// ports.NotFound. Used for both single-row (QueryRow) and multi-row (Rows)
// scan sites.
func scanUser(row rowScanner) (domain.User, error) {
	var u domain.User
	var role int16
	var status int16
	err := row.Scan(
		&u.ID, &u.Email, &u.Username, &u.DisplayName, &u.AvatarURL, &u.PasswordHash,
		&role, &status, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, ports.NotFound("user", u.ID)
		}
		return domain.User{}, fmt.Errorf("user_repo: scan: %w", err)
	}
	u.SystemRole = commonv1.SystemRole(role)
	u.Status = domain.UserStatus(status)
	return u, nil
}

// Compile-time interface assertion.
var _ ports.UserRepo = (*UserRepo)(nil)
