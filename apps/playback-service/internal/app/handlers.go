package app

import (
	"context"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	playbackv1 "vibesync/gen/go/vibesync/playback/v1"
	vberr "vibesync/libs/errors"

	playbackv1connect "vibesync/gen/go/vibesync/playback/v1/playbackv1connect"
)

// LoadMedia records the loaded media for a room. Full media loading/queue
// management is Phase 9; Phase 8 just caches the media_id.
func (s *Service) LoadMedia(
	ctx context.Context,
	req *connect.Request[playbackv1.LoadMediaRequest],
) (*connect.Response[playbackv1.LoadMediaResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	target := req.Msg.GetTarget()
	roomID := target.GetRoomId().GetValue()
	mediaID := target.GetMediaId().GetValue()
	if roomID == "" || mediaID == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.playback", "MISSING_TARGET", "room_id and media_id required")
	}

	now := s.clock.Now()
	s.cache.LoadMedia(roomID, mediaID, now)

	// Best-effort persist (the cache is the hot path; persistence is async).
	_ = s.persistRoom(ctx, roomID)

	return connect.NewResponse(&playbackv1.LoadMediaResponse{Accepted: true}), nil
}

// ApplySyncCommand applies an authoritative SyncState with fencing-token
// enforcement. This is the Playback Service's core responsibility per
// algorithm.md §Host migration step 4: reject stale commands after migration.
func (s *Service) ApplySyncCommand(
	ctx context.Context,
	req *connect.Request[playbackv1.ApplySyncCommandRequest],
) (*connect.Response[playbackv1.ApplySyncCommandResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	roomID := req.Msg.GetRoomId().GetValue()
	state := req.Msg.GetState()
	if roomID == "" || state == nil {
		return nil, vberr.InvalidArgumentFor("vibesync.playback", "MISSING_STATE", "room_id and state required")
	}

	now := s.clock.Now()
	applied := s.cache.ApplySyncCommand(
		roomID,
		state.GetMediaId().GetValue(),
		int16(state.GetStatus()),
		state.GetMediaTimeMs(),
		state.GetWallTimeMs(),
		state.GetPlaybackRate(),
		state.GetEpoch(),
		state.GetHostId().GetValue(),
		state.GetFencingToken(),
		now,
	)

	return connect.NewResponse(&playbackv1.ApplySyncCommandResponse{Applied: applied}), nil
}

// persistRoom writes the cached room state to Postgres (best-effort). The
// cache is the hot path; this checkpoint is for restart recovery.
func (s *Service) persistRoom(ctx context.Context, roomID string) error {
	room, ok := s.cache.Get(roomID)
	if !ok {
		return nil
	}
	return s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return s.repo.Upsert(ctx, tx, room)
	})
}

// Compile-time assertion.
var _ playbackv1connect.PlaybackServiceHandler = (*Service)(nil)
