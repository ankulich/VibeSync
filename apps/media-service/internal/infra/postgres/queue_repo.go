package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/media-service/internal/domain"
	"vibesync/apps/media-service/internal/ports"
)

// QueueRepo implements ports.QueueRepo.
type QueueRepo struct{}

// NewQueueRepo constructs a QueueRepo.
func NewQueueRepo() *QueueRepo { return &QueueRepo{} }

// Add appends a media item to a room's queue. The position is computed as
// MAX(position)+1 for the room (0 for the first item) inside the transaction so
// concurrent appends within separate transactions do not collide. The added_at
// timestamp is taken from the column default.
func (QueueRepo) Add(ctx context.Context, tx pgx.Tx, roomID, mediaID string) (domain.QueueItem, error) {
	var item domain.QueueItem
	err := tx.QueryRow(ctx, `
		INSERT INTO media_queue (room_id, position, media_id)
		SELECT $1, COALESCE(MAX(position), -1) + 1, $2
		  FROM media_queue WHERE room_id = $1
		RETURNING room_id, position, media_id, added_at`,
		roomID, mediaID).Scan(&item.RoomID, &item.Position, &item.MediaID, &item.AddedAt)
	if err != nil {
		return domain.QueueItem{}, fmt.Errorf("queue_repo: add: %w", err)
	}
	return item, nil
}

// List returns the queue items for a room ordered by position ascending.
func (QueueRepo) List(ctx context.Context, tx pgx.Tx, roomID string) ([]domain.QueueItem, error) {
	rows, err := tx.Query(ctx, `
		SELECT room_id, position, media_id, added_at
		  FROM media_queue WHERE room_id = $1 ORDER BY position`, roomID)
	if err != nil {
		return nil, fmt.Errorf("queue_repo: list: %w", err)
	}
	defer rows.Close()
	var items []domain.QueueItem
	for rows.Next() {
		var it domain.QueueItem
		if err := rows.Scan(&it.RoomID, &it.Position, &it.MediaID, &it.AddedAt); err != nil {
			return nil, fmt.Errorf("queue_repo: scan: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("queue_repo: list rows: %w", err)
	}
	return items, nil
}

// Remove deletes the queue entry at the given position and renumbers the
// remaining entries so positions stay dense (0-based, gap-free). It returns
// ports.NotFound when no row exists at the given position.
func (QueueRepo) Remove(ctx context.Context, tx pgx.Tx, roomID string, position int) error {
	tag, err := tx.Exec(ctx, `DELETE FROM media_queue WHERE room_id = $1 AND position = $2`, roomID, position)
	if err != nil {
		return fmt.Errorf("queue_repo: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ports.NotFound("queue", fmt.Sprintf("%s/%d", roomID, position))
	}
	// Shift every entry after the removed slot down by one so positions stay dense.
	if _, err := tx.Exec(ctx, `
		UPDATE media_queue SET position = position - 1
		 WHERE room_id = $1 AND position > $2`, roomID, position); err != nil {
		return fmt.Errorf("queue_repo: renumber: %w", err)
	}
	return nil
}
