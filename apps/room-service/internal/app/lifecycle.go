package app

import (
	"context"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"vibesync/apps/room-service/internal/domain"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	roomv1 "vibesync/gen/go/vibesync/room/v1"
	roomv1connect "vibesync/gen/go/vibesync/room/v1/roomv1connect"
	vberr "vibesync/libs/errors"
	vboutbox "vibesync/libs/outbox"
)

// CreateRoom creates a new room. The caller becomes the owner. Emits
// room.created.v1 via the outbox for the Sync Service to initialize state.
func (s *Service) CreateRoom(ctx context.Context, req *connect.Request[roomv1.CreateRoomRequest]) (*connect.Response[roomv1.CreateRoomResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	subject := subjectFromHeader(req.Header())
	if subject.SystemRole < commonv1.SystemRole_SYSTEM_ROLE_USER {
		return nil, vberr.PermissionDenied("room.create", "rooms")
	}
	var maxM *int
	if req.Msg.MaxMembers != nil {
		v := int(*req.Msg.MaxMembers)
		maxM = &v
	}
	room, err := domain.NewRoom(s.now(), s.idgen.New(), subject.UserID, domain.NewRoomParams{
		Name:        req.Msg.Name,
		Description: req.Msg.GetDescription(),
		Visibility:  domain.RoomVisibility(req.Msg.Visibility),
		MaxMembers:  maxM,
	})
	if err != nil {
		return nil, vberr.InvalidArgumentFor("vibesync.room", "INVALID_ROOM", err.Error()).WithCause(err)
	}

	err = s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.rooms.Create(ctx, tx, room); err != nil {
			return err
		}
		// Owner is the first member with OWNER role.
		if err := s.members.Upsert(ctx, tx, domain.Member{
			RoomID: room.ID, UserID: subject.UserID,
			Role: commonv1.RoomRole_ROOM_ROLE_OWNER, JoinedAt: s.now(),
		}); err != nil {
			return err
		}
		if err := s.members.IncrementRoomMemberCount(ctx, tx, room.ID, 1); err != nil {
			return err
		}
		// For private/unlisted rooms, generate an invite code.
		if room.RequiresInvite() || room.Visibility == domain.VisibilityUnlisted {
			code := domain.GenerateInviteCode()
			if err := s.invites.Create(ctx, tx, code, room.ID, nil); err != nil {
				return err
			}
		}
		// Emit room.created.v1.
		payload, _ := json.Marshal(map[string]any{
			"room_id":    room.ID,
			"slug":       room.Slug,
			"name":       room.Name,
			"owner_id":   room.OwnerID,
			"visibility": int(room.Visibility),
		})
		return s.outbox.Append(ctx, tx, vboutbox.Event{
			ID: s.idgen.New(), AggregateID: room.ID, Topic: "room.created.v1",
			Key: room.ID, Payload: payload, OccurredAt: s.now(), Version: "v1",
		})
	})
	if err != nil {
		return nil, vberr.Internal("CREATE_ROOM_FAILED", err.Error()).WithCause(err)
	}
	room.MemberCount = 1
	return connect.NewResponse(&roomv1.CreateRoomResponse{Room: roomToProto(room)}), nil
}

// GetRoom fetches a room by ID or slug.
func (s *Service) GetRoom(ctx context.Context, req *connect.Request[roomv1.GetRoomRequest]) (*connect.Response[roomv1.GetRoomResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	var room domain.Room
	err := s.readTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var ferr error
		switch l := req.Msg.Lookup.(type) {
		case *roomv1.GetRoomRequest_Id:
			room, ferr = s.rooms.GetByID(ctx, tx, l.Id.GetValue())
		case *roomv1.GetRoomRequest_Slug:
			room, ferr = s.rooms.GetBySlug(ctx, tx, l.Slug)
		default:
			return vberr.InvalidArgumentFor("vibesync.room", "MISSING_LOOKUP", "id or slug required")
		}
		return ferr
	})
	if err != nil {
		if isNotFound(err) {
			return nil, vberr.NotFound("room", "")
		}
		return nil, vberr.Internal("GET_ROOM_FAILED", err.Error()).WithCause(err)
	}
	// Private rooms require membership.
	subject := subjectFromHeader(req.Header())
	if room.Visibility == domain.VisibilityPrivate {
		if _, err := s.checkMembership(ctx, room.ID, subject.UserID); err != nil {
			return nil, vberr.PermissionDenied("get_room", "room:"+room.ID)
		}
	}
	return connect.NewResponse(&roomv1.GetRoomResponse{Room: roomToProto(room)}), nil
}

// ListRooms lists rooms. Public rooms by default; admin can see all visibilities.
func (s *Service) ListRooms(ctx context.Context, req *connect.Request[roomv1.ListRoomsRequest]) (*connect.Response[roomv1.ListRoomsResponse], error) {
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
	subject := subjectFromHeader(req.Header())
	visibilities := []domain.RoomVisibility{domain.VisibilityPublic}
	if subject.SystemRole >= commonv1.SystemRole_SYSTEM_ROLE_ADMINISTRATOR {
		visibilities = []domain.RoomVisibility{
			domain.VisibilityPublic, domain.VisibilityUnlisted, domain.VisibilityPrivate,
		}
	}
	var rooms []domain.Room
	var nextCursor string
	err := s.readTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var ferr error
		rooms, nextCursor, ferr = s.rooms.List(ctx, tx, cursor, int(limit), search, visibilities)
		return ferr
	})
	if err != nil {
		return nil, vberr.Internal("LIST_ROOMS_FAILED", err.Error()).WithCause(err)
	}
	protoRooms := make([]*roomv1.Room, 0, len(rooms))
	for _, r := range rooms {
		protoRooms = append(protoRooms, roomToProto(r))
	}
	return connect.NewResponse(&roomv1.ListRoomsResponse{
		Rooms: protoRooms,
		Page:  &commonv1.PageResponse{NextCursor: nextCursor, Total: uint64(len(rooms))},
	}), nil
}

// JoinRoom adds the caller to a room. Private rooms require an invite code.
func (s *Service) JoinRoom(ctx context.Context, req *connect.Request[roomv1.JoinRoomRequest]) (*connect.Response[roomv1.JoinRoomResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	subject := subjectFromHeader(req.Header())
	if subject.SystemRole < commonv1.SystemRole_SYSTEM_ROLE_USER {
		return nil, vberr.PermissionDenied("room.join", "rooms")
	}
	roomID := req.Msg.GetRoomId().GetValue()
	if roomID == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.room", "MISSING_ROOM_ID", "room_id required")
	}
	now := s.now()
	var room domain.Room
	var role commonv1.RoomRole
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var ferr error
		room, ferr = s.rooms.GetByID(ctx, tx, roomID)
		if ferr != nil {
			return ferr
		}
		if room.RequiresInvite() {
			code := req.Msg.GetInviteCode()
			if code == "" {
				return vberr.PermissionDenied("room.join", "room:"+roomID)
			}
			inviteRoomID, err := s.invites.Get(ctx, tx, code)
			if err != nil || inviteRoomID != roomID {
				return vberr.PermissionDenied("room.join", "room:"+roomID)
			}
		}
		if room.IsFull() {
			return vberr.ResourceExhausted("vibesync.room", "ROOM_FULL", "room is at capacity")
		}
		// Check if already a member (idempotent join).
		existing, err := s.members.Get(ctx, tx, roomID, subject.UserID)
		if err == nil {
			role = existing.Role
			return nil // already a member
		}
		if !isNotFound(err) {
			return err
		}
		role = commonv1.RoomRole_ROOM_ROLE_MEMBER
		if err := s.members.Upsert(ctx, tx, domain.Member{
			RoomID: roomID, UserID: subject.UserID, Role: role, JoinedAt: now,
		}); err != nil {
			return err
		}
		if err := s.members.IncrementRoomMemberCount(ctx, tx, roomID, 1); err != nil {
			return err
		}
		room.MemberCount++
		// Emit room.joined.v1.
		payload, _ := json.Marshal(map[string]any{
			"room_id": roomID, "user_id": subject.UserID,
			"role": int(role), "joined_at": now.UTC().Format(time.RFC3339),
		})
		return s.outbox.Append(ctx, tx, vboutbox.Event{
			ID: s.idgen.New(), AggregateID: roomID, Topic: "room.joined.v1",
			Key: roomID, Payload: payload, OccurredAt: now, Version: "v1",
		})
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&roomv1.JoinRoomResponse{
		Room: roomToProto(room), AssignedRole: role,
	}), nil
}

// UpdateRoom updates mutable room fields. Owner only.
func (s *Service) UpdateRoom(ctx context.Context, req *connect.Request[roomv1.UpdateRoomRequest]) (*connect.Response[roomv1.UpdateRoomResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	subject := subjectFromHeader(req.Header())
	roomID := req.Msg.GetId().GetValue()
	var room domain.Room
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var ferr error
		room, ferr = s.rooms.GetByID(ctx, tx, roomID)
		if ferr != nil {
			return ferr
		}
		if err := s.requireRoomRole(ctx, tx, roomID, subject, commonv1.RoomRole_ROOM_ROLE_OWNER); err != nil {
			return err
		}
		var vis *domain.RoomVisibility
		if req.Msg.Visibility != nil {
			v := domain.RoomVisibility(*req.Msg.Visibility)
			vis = &v
		}
		room.ApplyUpdate(s.now(), req.Msg.Name, req.Msg.Description,
			vis, req.Msg.MaxMembers)
		return s.rooms.Update(ctx, tx, room)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&roomv1.UpdateRoomResponse{Room: roomToProto(room)}), nil
}

// DeleteRoom removes a room. Owner only. Emits room.deleted.v1.
func (s *Service) DeleteRoom(ctx context.Context, req *connect.Request[roomv1.DeleteRoomRequest]) (*connect.Response[roomv1.DeleteRoomResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	subject := subjectFromHeader(req.Header())
	roomID := req.Msg.GetId().GetValue()
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.requireRoomRole(ctx, tx, roomID, subject, commonv1.RoomRole_ROOM_ROLE_OWNER); err != nil {
			return err
		}
		if err := s.rooms.Delete(ctx, tx, roomID); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{
			"room_id": roomID, "deleted_at": s.now().UTC().Format(time.RFC3339),
		})
		return s.outbox.Append(ctx, tx, vboutbox.Event{
			ID: s.idgen.New(), AggregateID: roomID, Topic: "room.deleted.v1",
			Key: roomID, Payload: payload, OccurredAt: s.now(), Version: "v1",
		})
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&roomv1.DeleteRoomResponse{}), nil
}

// roomToProto converts a domain Room to the proto message.
func roomToProto(r domain.Room) *roomv1.Room {
	return &roomv1.Room{
		Id:          &commonv1.Id{Value: r.ID},
		Slug:        r.Slug,
		Name:        r.Name,
		Description: r.Description,
		Visibility:  roomv1.RoomVisibility(r.Visibility),
		OwnerId:     &commonv1.Id{Value: r.OwnerID},
		MaxMembers:  uint32(r.MaxMembers),
		MemberCount: uint32(r.MemberCount),
		CreatedAt:   timestamppb.New(r.CreatedAt),
		UpdatedAt:   timestamppb.New(r.UpdatedAt),
	}
}

// Compile-time assertion that *Service satisfies the full handler interface.
var _ roomv1connect.RoomServiceHandler = (*Service)(nil)
