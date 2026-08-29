package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/media-service/internal/domain"
)

// MediaRepo implements ports.MediaRepo.
type MediaRepo struct{}

// NewMediaRepo constructs a MediaRepo.
func NewMediaRepo() *MediaRepo { return &MediaRepo{} }

// mediaColumns is the canonical column list for media_items, in scan order.
const mediaColumns = `id, kind, source, external_ref, title, artist, duration_ms, cover_url, created_at`

// Create inserts a new media_items row.
func (MediaRepo) Create(ctx context.Context, tx pgx.Tx, m domain.Media) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO media_items (id, kind, source, external_ref, title, artist, duration_ms, cover_url, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		m.ID, int16(m.Kind), int16(m.Source), m.ExternalRef, m.Title, m.Artist,
		m.DurationMs, m.CoverURL, m.CreatedAt)
	if err != nil {
		return fmt.Errorf("media_repo: create: %w", err)
	}
	return nil
}

// GetByID loads a media item by its ID.
func (MediaRepo) GetByID(ctx context.Context, tx pgx.Tx, id string) (domain.Media, error) {
	row := tx.QueryRow(ctx, `SELECT `+mediaColumns+` FROM media_items WHERE id = $1`, id)
	return scanMedia(row, id)
}

// List returns a page of media items filtered by an optional title search,
// ordered by created_at descending. The cursor is the last ID of the previous
// page; the returned string is the next page cursor (empty when there are no
// more rows).
func (MediaRepo) List(ctx context.Context, tx pgx.Tx, cursor string, limit int, search string) ([]domain.Media, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var args []any
	var conds []string
	if search != "" {
		args = append(args, "%"+strings.ToLower(search)+"%")
		conds = append(conds, fmt.Sprintf("LOWER(title) ILIKE $%d", len(args)))
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
	query := `SELECT ` + mediaColumns + ` FROM media_items` + where + fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("media_repo: list: %w", err)
	}
	defer rows.Close()
	var items []domain.Media
	for rows.Next() {
		m, err := scanMedia(rows, "")
		if err != nil {
			return nil, "", err
		}
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("media_repo: list rows: %w", err)
	}
	nextCursor := ""
	if len(items) > limit {
		nextCursor = items[limit].ID
		items = items[:limit]
	}
	return items, nextCursor, nil
}

// rowScanner is the minimal Scan surface shared by QueryRow and Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanMedia(row rowScanner, idForErr string) (domain.Media, error) {
	var m domain.Media
	var kind, source int16
	err := row.Scan(&m.ID, &kind, &source, &m.ExternalRef, &m.Title, &m.Artist,
		&m.DurationMs, &m.CoverURL, &m.CreatedAt)
	if err != nil {
		return domain.Media{}, mapErr("media", idForErr, err)
	}
	m.Kind = domain.MediaKind(kind)
	m.Source = domain.MediaSource(source)
	return m, nil
}
