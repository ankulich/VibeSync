package app

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"vibesync/apps/media-service/internal/domain"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	mediav1 "vibesync/gen/go/vibesync/media/v1"
	mediav1connect "vibesync/gen/go/vibesync/media/v1/mediav1connect"
	roomv1 "vibesync/gen/go/vibesync/room/v1"
	vberr "vibesync/libs/errors"
)

// GetMedia loads a single media item by ID.
func (s *Service) GetMedia(ctx context.Context, req *connect.Request[mediav1.GetMediaRequest]) (*connect.Response[mediav1.GetMediaResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	id := req.Msg.GetId().GetValue()
	var media domain.Media
	err := s.readTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var ferr error
		media, ferr = s.media.GetByID(ctx, tx, id)
		return ferr
	})
	if err != nil {
		if isNotFound(err) {
			return nil, vberr.NotFound("media", id)
		}
		return nil, vberr.Internal("GET_MEDIA_FAILED", err.Error()).WithCause(err)
	}
	return connect.NewResponse(&mediav1.GetMediaResponse{Media: mediaToProto(media)}), nil
}

// CreateMedia validates the request, constructs a Media via the domain
// constructor, persists it, and returns the created item.
func (s *Service) CreateMedia(ctx context.Context, req *connect.Request[mediav1.CreateMediaRequest]) (*connect.Response[mediav1.CreateMediaResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	subject := subjectFromHeader(req.Header())
	if subject.SystemRole < commonv1.SystemRole_SYSTEM_ROLE_USER {
		return nil, vberr.PermissionDenied("media.create", "media")
	}
	media, err := domain.NewMedia(s.now(), s.idgen.New(), domain.NewMediaParams{
		Kind:        domain.MediaKind(req.Msg.Kind),
		Source:      domain.MediaSource(req.Msg.Source),
		ExternalRef: req.Msg.ExternalRef,
		Title:       req.Msg.Title,
		Artist:      req.Msg.Artist,
		DurationMs:  int64(req.Msg.DurationMs),
		CoverURL:    req.Msg.CoverUrl,
	})
	if err != nil {
		return nil, vberr.InvalidArgumentFor("vibesync.media", "INVALID_MEDIA", err.Error()).WithCause(err)
	}
	err = s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return s.media.Create(ctx, tx, media)
	})
	if err != nil {
		return nil, vberr.Internal("CREATE_MEDIA_FAILED", err.Error()).WithCause(err)
	}
	return connect.NewResponse(&mediav1.CreateMediaResponse{Media: mediaToProto(media)}), nil
}

// ListMedia returns a page of media items with optional title search and
// cursor-based pagination.
func (s *Service) ListMedia(ctx context.Context, req *connect.Request[mediav1.ListMediaRequest]) (*connect.Response[mediav1.ListMediaResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	cursor := ""
	limit := uint32(50)
	if req.Msg.GetPage() != nil {
		cursor = req.Msg.GetPage().GetCursor()
		if l := req.Msg.GetPage().GetLimit(); l > 0 {
			limit = l
		}
	}
	search := req.Msg.GetSearch()
	var items []domain.Media
	var nextCursor string
	err := s.readTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var ferr error
		items, nextCursor, ferr = s.media.List(ctx, tx, cursor, int(limit), search)
		return ferr
	})
	if err != nil {
		return nil, vberr.Internal("LIST_MEDIA_FAILED", err.Error()).WithCause(err)
	}
	protos := make([]*mediav1.Media, 0, len(items))
	for _, m := range items {
		protos = append(protos, mediaToProto(m))
	}
	return connect.NewResponse(&mediav1.ListMediaResponse{
		Media: protos,
		Page:  &commonv1.PageResponse{NextCursor: nextCursor, Total: uint64(len(items))},
	}), nil
}

// AddToQueue appends a media item to a room's queue. The room owner and
// members granted ADD_QUEUE may add (ADR-0017).
func (s *Service) AddToQueue(ctx context.Context, req *connect.Request[mediav1.AddToQueueRequest]) (*connect.Response[mediav1.AddToQueueResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	subject := subjectFromHeader(req.Header())
	if subject.SystemRole < commonv1.SystemRole_SYSTEM_ROLE_USER {
		return nil, vberr.PermissionDenied("media.queue.add", "media-queue")
	}
	roomID := req.Msg.GetRoomId().GetValue()
	mediaID := req.Msg.GetMediaId().GetValue()
	if roomID == "" || mediaID == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.media", "MISSING_ID", "room_id and media_id required")
	}
	if err := s.requireQueuePermission(ctx, subject.UserID, roomID, roomv1.RoomPermission_ROOM_PERMISSION_ADD_QUEUE, "media.queue.add"); err != nil {
		return nil, err
	}
	var item domain.QueueItem
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Confirm the media exists before referencing it (clean NotFound vs FK error).
		if _, err := s.media.GetByID(ctx, tx, mediaID); err != nil {
			return err
		}
		var ferr error
		item, ferr = s.queue.Add(ctx, tx, roomID, mediaID)
		return ferr
	})
	if err != nil {
		if isNotFound(err) {
			return nil, vberr.NotFound("media", mediaID)
		}
		return nil, vberr.Internal("ADD_TO_QUEUE_FAILED", err.Error()).WithCause(err)
	}
	return connect.NewResponse(&mediav1.AddToQueueResponse{Item: queueItemToProto(item)}), nil
}

// requireQueuePermission enforces an owner-granted queue permission
// (ADR-0017); the room owner passes every check.
func (s *Service) requireQueuePermission(ctx context.Context, userID, roomID string, perm roomv1.RoomPermission, action string) error {
	if userID == "" {
		return vberr.Unauthenticated("MISSING_USER_ID")
	}
	allowed, err := s.perms.Has(ctx, roomID, userID, perm)
	if err != nil {
		return vberr.Internal("PERMISSION_CHECK_FAILED", err.Error()).WithCause(err)
	}
	if !allowed {
		return vberr.PermissionDenied(action, "room:"+roomID)
	}
	return nil
}

// GetQueue lists the queue items for a room, ordered by position.
func (s *Service) GetQueue(ctx context.Context, req *connect.Request[mediav1.GetQueueRequest]) (*connect.Response[mediav1.GetQueueResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	roomID := req.Msg.GetRoomId().GetValue()
	var items []domain.QueueItem
	err := s.readTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var ferr error
		items, ferr = s.queue.List(ctx, tx, roomID)
		return ferr
	})
	if err != nil {
		return nil, vberr.Internal("GET_QUEUE_FAILED", err.Error()).WithCause(err)
	}
	protos := make([]*mediav1.QueueItem, 0, len(items))
	for _, it := range items {
		protos = append(protos, queueItemToProto(it))
	}
	return connect.NewResponse(&mediav1.GetQueueResponse{Items: protos}), nil
}

// RemoveFromQueue removes the queue entry at the given position and renumbers
// the remaining entries. The room owner and members granted REMOVE_QUEUE
// may remove (ADR-0017).
func (s *Service) RemoveFromQueue(ctx context.Context, req *connect.Request[mediav1.RemoveFromQueueRequest]) (*connect.Response[mediav1.RemoveFromQueueResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	subject := subjectFromHeader(req.Header())
	if subject.SystemRole < commonv1.SystemRole_SYSTEM_ROLE_USER {
		return nil, vberr.PermissionDenied("media.queue.remove", "media-queue")
	}
	roomID := req.Msg.GetRoomId().GetValue()
	position := int(req.Msg.GetPosition())
	if err := s.requireQueuePermission(ctx, subject.UserID, roomID, roomv1.RoomPermission_ROOM_PERMISSION_REMOVE_QUEUE, "media.queue.remove"); err != nil {
		return nil, err
	}
	removed := false
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.queue.Remove(ctx, tx, roomID, position); err != nil {
			return err
		}
		removed = true
		return nil
	})
	if err != nil {
		if isNotFound(err) {
			return nil, vberr.NotFound("queue", fmt.Sprintf("%s:%d", roomID, position))
		}
		return nil, vberr.Internal("REMOVE_FROM_QUEUE_FAILED", err.Error()).WithCause(err)
	}
	return connect.NewResponse(&mediav1.RemoveFromQueueResponse{Removed: removed}), nil
}

// mediaToProto converts a domain Media to its proto representation.
func mediaToProto(m domain.Media) *mediav1.Media {
	return &mediav1.Media{
		Id:          &commonv1.Id{Value: m.ID},
		Kind:        mediav1.MediaKind(m.Kind),
		Source:      mediav1.MediaSource(m.Source),
		ExternalRef: m.ExternalRef,
		Title:       m.Title,
		Artist:      m.Artist,
		Duration:    durationpb.New(time.Duration(m.DurationMs) * time.Millisecond),
		CoverUrl:    m.CoverURL,
		CreatedAt:   timestamppb.New(m.CreatedAt),
	}
}

// queueItemToProto converts a domain QueueItem to its proto representation.
func queueItemToProto(q domain.QueueItem) *mediav1.QueueItem {
	return &mediav1.QueueItem{
		MediaId:  &commonv1.Id{Value: q.MediaID},
		Position: uint32(q.Position),
		AddedAt:  timestamppb.New(q.AddedAt),
	}
}

// Compile-time assertion that *Service satisfies the full handler interface.
var _ mediav1connect.MediaServiceHandler = (*Service)(nil)
