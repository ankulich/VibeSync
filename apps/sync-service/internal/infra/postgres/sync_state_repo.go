package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/sync-service/internal/domain"
)

// SyncStateRepo implements ports.SyncStateRepo.
type SyncStateRepo struct{}

// NewSyncStateRepo returns a SyncStateRepo.
func NewSyncStateRepo() *SyncStateRepo { return &SyncStateRepo{} }

// Upsert persists the authoritative state for restart recovery.
func (SyncStateRepo) Upsert(ctx context.Context, tx pgx.Tx, s domain.SyncState) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO sync_state (room_id, media_id, status, media_time_ms, wall_time_ms,
		    playback_rate, epoch, host_id, fencing_token, owner_id, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (room_id) DO UPDATE SET
		    media_id=EXCLUDED.media_id, status=EXCLUDED.status,
		    media_time_ms=EXCLUDED.media_time_ms, wall_time_ms=EXCLUDED.wall_time_ms,
		    playback_rate=EXCLUDED.playback_rate, epoch=EXCLUDED.epoch,
		    host_id=EXCLUDED.host_id, fencing_token=EXCLUDED.fencing_token,
		    owner_id=EXCLUDED.owner_id, updated_at=EXCLUDED.updated_at`,
		s.RoomID, s.MediaID, int16(s.Status), s.MediaTimeMs, s.WallTimeMs,
		s.PlaybackRate, int64(s.Epoch), s.HostID, int64(s.FencingToken), s.OwnerID, time.Now())
	if err != nil {
		return fmt.Errorf("sync_state_repo: upsert: %w", err)
	}
	return nil
}

// Get loads the persisted state for a room.
func (SyncStateRepo) Get(ctx context.Context, tx pgx.Tx, roomID string) (domain.SyncState, error) {
	row := tx.QueryRow(ctx, `
		SELECT room_id, media_id, status, media_time_ms, wall_time_ms,
		       playback_rate, epoch, host_id, fencing_token, owner_id, updated_at
		  FROM sync_state WHERE room_id = $1`, roomID)
	var s domain.SyncState
	var status int16
	var epoch, fencingToken int64
	var updatedAt time.Time
	err := row.Scan(&s.RoomID, &s.MediaID, &status, &s.MediaTimeMs, &s.WallTimeMs,
		&s.PlaybackRate, &epoch, &s.HostID, &fencingToken, &s.OwnerID, &updatedAt)
	if err != nil {
		return domain.SyncState{}, fmt.Errorf("sync_state_repo: get: %w", err)
	}
	s.Status = domain.PlaybackStatus(status)
	s.Epoch = uint64(epoch)
	s.FencingToken = uint64(fencingToken)
	s.EpochStarted = updatedAt
	return s, nil
}
