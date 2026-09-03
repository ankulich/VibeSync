package app

import (
	"context"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	"vibesync/apps/room-service/internal/domain"
	roomv1 "vibesync/gen/go/vibesync/room/v1"
	vberr "vibesync/libs/errors"
)

// GrantPermissions replaces a member's permission set. Room owner only; the
// owner themselves implicitly holds everything and cannot be granted (the
// request is rejected as invalid argument).
func (s *Service) GrantPermissions(
	ctx context.Context,
	req *connect.Request[roomv1.GrantPermissionsRequest],
) (*connect.Response[roomv1.GrantPermissionsResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	subject := subjectFromHeader(req.Header())
	roomID := req.Msg.GetRoomId().GetValue()
	targetID := req.Msg.GetUserId().GetValue()
	perms := domain.PermissionsFromProto(req.Msg.GetPermissions())

	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		room, err := s.rooms.GetByID(ctx, tx, roomID)
		if err != nil {
			return err
		}
		if room.OwnerID != subject.UserID {
			return vberr.PermissionDenied("grant_permissions", "room:"+roomID)
		}
		if targetID == subject.UserID {
			return vberr.InvalidArgumentFor("vibesync.room", "GRANT_SELF",
				"the owner already holds every permission")
		}
		// The target must be a member of the room.
		if _, err := s.members.Get(ctx, tx, roomID, targetID); err != nil {
			if isNotFound(err) {
				return vberr.NotFound("member", targetID)
			}
			return err
		}
		return s.perms.Set(ctx, tx, roomID, targetID, perms)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&roomv1.GrantPermissionsResponse{
		Permissions: perms.ToProto(),
	}), nil
}

// GetMemberPermissions answers a single member's effective grants plus the
// owner flag. Used by the Sync and Media Services for authorization and by
// the UI; owner ⇒ all permissions regardless of the stored set.
func (s *Service) GetMemberPermissions(
	ctx context.Context,
	req *connect.Request[roomv1.GetMemberPermissionsRequest],
) (*connect.Response[roomv1.GetMemberPermissionsResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	roomID := req.Msg.GetRoomId().GetValue()
	userID := req.Msg.GetUserId().GetValue()

	var (
		perms    domain.Permissions
		isOwner  bool
		notFound = false
	)
	err := s.readTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		room, err := s.rooms.GetByID(ctx, tx, roomID)
		if err != nil {
			if isNotFound(err) {
				notFound = true
				return nil
			}
			return err
		}
		isOwner = room.OwnerID == userID
		perms, err = s.perms.Get(ctx, tx, roomID, userID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if notFound {
		return nil, vberr.NotFound("room", roomID)
	}
	return connect.NewResponse(&roomv1.GetMemberPermissionsResponse{
		Permissions: perms.ToProto(),
		IsOwner:     isOwner,
	}), nil
}

// memberPermissionsFor augments GetMembers rows with each member's grants.
// The owner row carries no permissions (they hold all implicitly).
func (s *Service) memberPermissionsFor(ctx context.Context, tx pgx.Tx, roomID string) (map[string]domain.Permissions, error) {
	return s.perms.ListByRoom(ctx, tx, roomID)
}
