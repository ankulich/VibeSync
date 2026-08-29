package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/playback-service/internal/domain"
)

// PlaybackRepo implements ports.PlaybackRepo.
type PlaybackRepo struct{}

// NewPlaybackRepo returns a PlaybackRepo.
func NewPlaybackRepo() *PlaybackRepo { return &PlaybackRepo{} }

// Upsert persists the cached playback state for restart recovery.
func (PlaybackRepo) Upsert(ctx context.Context, tx pgx.Tx, r domain.PlaybackRoom) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO playback_state (room_id, media_id, status, media_time_ms, wall_time_ms,
		    playback_rate, epoch, host_id, fencing_token, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (room_id) DO UPDATE SET
		    media_id=EXCLUDED.media_id, status=EXCLUDED.status,
		    media_time_ms=EXCLUDED.media_time_ms, wall_time_ms=EXCLUDED.wall_time_ms,
		    playback_rate=EXCLUDED.playback_rate, epoch=EXCLUDED.epoch,
		    host_id=EXCLUDED.host_id, fencing_token=EXCLUDED.fencing_token,
		    updated_at=EXCLUDED.updated_at`,
		r.RoomID, r.MediaID, r.Status, r.MediaTimeMs, r.WallTimeMs,
		r.PlaybackRate, int64(r.Epoch), r.HostID, int64(r.FencingToken), time.Now())
	if err != nil {
		return fmt.Errorf("playback_repo: upsert: %w", err)
	}
	return nil
}

// Get loads the persisted playback state for a room.
func (PlaybackRepo) Get(ctx context.Context, tx pgx.Tx, roomID string) (domain.PlaybackRoom, error) {
	row := tx.QueryRow(ctx, `
		SELECT room_id, media_id, status, media_time_ms, wall_time_ms,
		       playback_rate, epoch, host_id, fencing_token, updated_at
		  FROM playback_state WHERE room_id = $1`, roomID)
	var r domain.PlaybackRoom
	var epoch, fencingToken int64
	err := row.Scan(&r.RoomID, &r.MediaID, &r.Status, &r.MediaTimeMs, &r.WallTimeMs,
		&r.PlaybackRate, &epoch, &r.HostID, &fencingToken, &r.UpdatedAt)
	if err != nil {
		return domain.PlaybackRoom{}, fmt.Errorf("playback_repo: get: %w", err)
	}
	r.Epoch = uint64(epoch)
	r.FencingToken = uint64(fencingToken)
	return r, nil
}
