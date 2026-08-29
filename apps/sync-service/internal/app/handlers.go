package app

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	syncv1 "vibesync/gen/go/vibesync/sync/v1"
	vberr "vibesync/libs/errors"
	vbkafka "vibesync/libs/kafka"

	"vibesync/apps/sync-service/internal/domain"
	syncv1connect "vibesync/gen/go/vibesync/sync/v1/syncv1connect"
)

// --- Subscribe (server-streaming) ---

// Subscribe holds a long-lived stream open for a client, forwarding
// authoritative updates, snapshots, and host-migration notices.
func (s *Service) Subscribe(
	ctx context.Context,
	req *connect.Request[syncv1.SubscribeRequest],
	stream *connect.ServerStream[syncv1.SubscribeResponse],
) error {
	if err := ctxDone(ctx); err != nil {
		return err
	}
	roomID := req.Msg.GetRoomId().GetValue()
	if roomID == "" {
		return vberr.InvalidArgumentFor("vibesync.sync", "MISSING_ROOM_ID", "room_id required")
	}

	room, err := s.manager.GetOrCreate(ctx, roomID)
	if err != nil {
		return vberr.Internal("SUBSCRIBE_FAILED", err.Error()).WithCause(err)
	}

	ch := room.RegisterSubscriber()
	defer room.UnregisterSubscriber(ch)

	// If the client is far behind, send a snapshot first.
	lastEpoch := req.Msg.GetLastAppliedEpoch()
	if lastEpoch > 0 && room.CurrentEpoch() > lastEpoch+uint64(s.cfg.Sync.RecoverRingBufferSize) {
		snap, _ := room.Recover(lastEpoch)
		if snap != nil {
			if err := stream.Send(&syncv1.SubscribeResponse{
				Payload: &syncv1.SubscribeResponse_Snapshot{Snapshot: snap},
			}); err != nil {
				return err
			}
		}
	}

	// Forward frames until the client disconnects.
	for {
		select {
		case <-ctx.Done():
			return nil
		case frame, ok := <-ch:
			if !ok {
				return nil // channel closed (room shutdown)
			}
			if err := stream.Send(frame); err != nil {
				return err
			}
		}
	}
}

// --- Heartbeat (unary) ---

// Heartbeat processes a client heartbeat, updating RTT/offset/drift estimates.
func (s *Service) Heartbeat(
	ctx context.Context,
	req *connect.Request[syncv1.HeartbeatRequest],
) (*connect.Response[syncv1.HeartbeatResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	roomID := req.Msg.GetRoomId().GetValue()
	if roomID == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.sync", "MISSING_ROOM_ID", "room_id required")
	}
	subject := subjectFromHeader(req.Header())
	if subject.UserID == "" {
		return nil, vberr.Unauthenticated("MISSING_USER_ID")
	}

	room, err := s.manager.GetOrCreate(ctx, roomID)
	if err != nil {
		return nil, vberr.Internal("HEARTBEAT_FAILED", err.Error()).WithCause(err)
	}

	serverWallMs, serverMediaMs, epoch, driftMs, rttMs := room.ProcessHeartbeat(
		roomID, subject.UserID,
		req.Msg.GetClientEpoch(),
		req.Msg.GetClientMediaTimeMs(),
		req.Msg.GetClientWallTimeMs(),
		req.Msg.GetLastServerWallTimeMs(),
	)

	return connect.NewResponse(&syncv1.HeartbeatResponse{
		ServerWallTimeMs:  serverWallMs,
		ServerMediaTimeMs: serverMediaMs,
		Epoch:             epoch,
		ClientDriftMs:     driftMs,
		SmoothedRttMs:     rttMs,
	}), nil
}

// --- Command (unary, fencing-token checked) ---

// Command applies an authoritative playback command (play/pause/seek/etc.).
// The originator must be the host with a valid fencing token.
func (s *Service) Command(
	ctx context.Context,
	req *connect.Request[syncv1.CommandRequest],
) (*connect.Response[syncv1.CommandResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	roomID := req.Msg.GetRoomId().GetValue()
	if roomID == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.sync", "MISSING_ROOM_ID", "room_id required")
	}
	subject := subjectFromHeader(req.Header())
	if subject.UserID == "" {
		return nil, vberr.Unauthenticated("MISSING_USER_ID")
	}

	room, err := s.manager.GetOrCreate(ctx, roomID)
	if err != nil {
		return nil, vberr.Internal("COMMAND_FAILED", err.Error()).WithCause(err)
	}

	kind := domain.CommandKind(req.Msg.GetKind())
	epoch, accepted, reason := room.ProcessCommand(
		subject.UserID, kind,
		req.Msg.SeekToMs, req.Msg.Rate,
		req.Msg.GetMediaId().GetValue(),
		req.Msg.GetFencingToken(),
	)

	return connect.NewResponse(&syncv1.CommandResponse{
		Epoch:    epoch,
		Accepted: accepted,
		Reason:   reason,
	}), nil
}

// --- Recover (unary) ---

// Recover replays buffered frames or returns a full snapshot after a
// reconnect.
func (s *Service) Recover(
	ctx context.Context,
	req *connect.Request[syncv1.RecoverRequest],
) (*connect.Response[syncv1.RecoverResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	roomID := req.Msg.GetRoomId().GetValue()
	if roomID == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.sync", "MISSING_ROOM_ID", "room_id required")
	}

	room, err := s.manager.GetOrCreate(ctx, roomID)
	if err != nil {
		return nil, vberr.Internal("RECOVER_FAILED", err.Error()).WithCause(err)
	}

	snapshot, frames := room.Recover(req.Msg.GetSinceEpoch())
	if snapshot != nil {
		return connect.NewResponse(&syncv1.RecoverResponse{
			Payload: &syncv1.RecoverResponse_Snapshot{Snapshot: snapshot},
		}), nil
	}
	return connect.NewResponse(&syncv1.RecoverResponse{
		Payload: &syncv1.RecoverResponse_Frames{
			Frames: &syncv1.SubscribeResponseBatch{Frames: frames},
		},
	}), nil
}

// --- Consumer handler (room.created.v1 → Init room) ---

// RoomCreatedV1 is the deserialized payload of a room.created.v1 event.
type RoomCreatedV1 struct {
	RoomID     string `json:"room_id"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	OwnerID    string `json:"owner_id"`
	Visibility int    `json:"visibility"`
}

// TxRunner runs a function inside a Postgres tx (for the consumer handler).
type TxRunner func(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error

// RoomCreatedHandler consumes room.created.v1 events and initializes the
// RoomSync for each new room.
type RoomCreatedHandler struct {
	manager *RoomManager
	logger  Logger
}

// NewRoomCreatedHandler constructs the consumer handler.
func NewRoomCreatedHandler(manager *RoomManager, logger Logger) *RoomCreatedHandler {
	return &RoomCreatedHandler{manager: manager, logger: logger}
}

// Handle deserializes the event and initializes the room.
func (h *RoomCreatedHandler) Handle(ctx context.Context, msg vbkafka.Message) error {
	var event RoomCreatedV1
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		h.logger.Error("consumer: malformed room.created.v1 payload, skipping",
			"err", err, "topic", msg.Topic, "offset", msg.Offset)
		return nil // skip poison messages
	}
	if event.RoomID == "" || event.OwnerID == "" {
		h.logger.Error("consumer: invalid room.created.v1 event, skipping",
			"room_id", event.RoomID, "owner_id", event.OwnerID)
		return nil
	}
	if err := h.manager.Init(ctx, event.RoomID, event.OwnerID); err != nil {
		h.logger.Error("consumer: init room failed", "err", err, "room_id", event.RoomID)
		return err // will retry
	}
	h.logger.Info("consumer: initialized room from room.created.v1",
		"room_id", event.RoomID, "owner_id", event.OwnerID)
	return nil
}

// CurrentEpoch returns the room's current epoch (for the Subscribe handler's
// initial-frame decision).
func (rs *RoomSync) CurrentEpoch() uint64 {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.state.Epoch
}

// Compile-time checks.
var (
	_ syncv1connect.SyncServiceHandler = (*Service)(nil)
	_ vbkafka.Handler                  = (*RoomCreatedHandler)(nil)
)
