package app

import (
	"context"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"vibesync/apps/room-service/internal/domain"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	roomv1 "vibesync/gen/go/vibesync/room/v1"
	vberr "vibesync/libs/errors"
)

// LeaveRoom removes the caller from a room. Self-service.
func (s *Service) LeaveRoom(ctx context.Context, req *connect.Request[roomv1.LeaveRoomRequest]) (*connect.Response[roomv1.LeaveRoomResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	subject := subjectFromHeader(req.Header())
	roomID := req.Msg.GetRoomId().GetValue()
	left := false
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		room, err := s.rooms.GetByID(ctx, tx, roomID)
		if err != nil {
			return err
		}
		// Owner cannot leave (must delete or transfer). They can leave if they
		// are not the owner.
		if room.OwnerID == subject.UserID {
			return vberr.FailedPrecondition("vibesync.room", "OWNER_CANNOT_LEAVE",
				"room owner must delete or transfer the room before leaving")
		}
		// Check membership.
		if _, err := s.members.Get(ctx, tx, roomID, subject.UserID); err != nil {
			if isNotFound(err) {
				return nil // idempotent: not a member, nothing to leave
			}
			return err
		}
		if err := s.members.Delete(ctx, tx, roomID, subject.UserID); err != nil {
			return err
		}
		return s.members.IncrementRoomMemberCount(ctx, tx, roomID, -1)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&roomv1.LeaveRoomResponse{Left: left}), nil
}

// GetMembers lists the members of a room. Any member may view.
func (s *Service) GetMembers(ctx context.Context, req *connect.Request[roomv1.GetMembersRequest]) (*connect.Response[roomv1.GetMembersResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	subject := subjectFromHeader(req.Header())
	roomID := req.Msg.GetRoomId().GetValue()
	var members []domain.Member
	err := s.readTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Must be a member to view (or admin).
		if subject.SystemRole < commonv1.SystemRole_SYSTEM_ROLE_ADMINISTRATOR {
			if _, err := s.members.Get(ctx, tx, roomID, subject.UserID); err != nil {
				if isNotFound(err) {
					return vberr.PermissionDenied("get_members", "room:"+roomID)
				}
				return err
			}
		}
		var ferr error
		members, ferr = s.members.List(ctx, tx, roomID)
		return ferr
	})
	if err != nil {
		return nil, err
	}
	protoMembers := make([]*roomv1.Member, 0, len(members))
	for _, m := range members {
		protoMembers = append(protoMembers, &roomv1.Member{
			UserId:   &commonv1.Id{Value: m.UserID},
			Role:     m.Role,
			JoinedAt: timestamppb.New(m.JoinedAt),
		})
	}
	return connect.NewResponse(&roomv1.GetMembersResponse{Members: protoMembers}), nil
}

// KickMember removes a user from the room. Requires MODERATOR or OWNER.
// Cannot kick the OWNER.
func (s *Service) KickMember(ctx context.Context, req *connect.Request[roomv1.KickMemberRequest]) (*connect.Response[roomv1.KickMemberResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	subject := subjectFromHeader(req.Header())
	roomID := req.Msg.GetRoomId().GetValue()
	targetID := req.Msg.GetUserId().GetValue()
	kicked := false
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		byMember, err := s.members.Get(ctx, tx, roomID, subject.UserID)
		if err != nil {
			if isNotFound(err) {
				return vberr.PermissionDenied("kick_member", "room:"+roomID)
			}
			return err
		}
		target, err := s.members.Get(ctx, tx, roomID, targetID)
		if err != nil {
			if isNotFound(err) {
				return vberr.NotFound("member", targetID)
			}
			return err
		}
		if !domain.CanKick(byMember.Role, target.Role) {
			return vberr.PermissionDenied("kick_member", "user:"+targetID)
		}
		if err := s.members.Delete(ctx, tx, roomID, targetID); err != nil {
			return err
		}
		if err := s.members.IncrementRoomMemberCount(ctx, tx, roomID, -1); err != nil {
			return err
		}
		kicked = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&roomv1.KickMemberResponse{Kicked: kicked}), nil
}

// UpdateMemberRole changes a member's room role. Owner only.
func (s *Service) UpdateMemberRole(ctx context.Context, req *connect.Request[roomv1.UpdateMemberRoleRequest]) (*connect.Response[roomv1.UpdateMemberRoleResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	subject := subjectFromHeader(req.Header())
	roomID := req.Msg.GetRoomId().GetValue()
	targetID := req.Msg.GetUserId().GetValue()
	newRole := req.Msg.GetRole()
	var updated domain.Member
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		byMember, err := s.members.Get(ctx, tx, roomID, subject.UserID)
		if err != nil {
			if isNotFound(err) {
				return vberr.PermissionDenied("update_member_role", "room:"+roomID)
			}
			return err
		}
		if !domain.CanPromote(byMember.Role, newRole) {
			return vberr.PermissionDenied("update_member_role", "user:"+targetID)
		}
		target, err := s.members.Get(ctx, tx, roomID, targetID)
		if err != nil {
			if isNotFound(err) {
				return vberr.NotFound("member", targetID)
			}
			return err
		}
		target.Role = newRole
		if err := s.members.UpdateRole(ctx, tx, roomID, targetID, newRole); err != nil {
			return err
		}
		updated = target
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&roomv1.UpdateMemberRoleResponse{
		Member: &roomv1.Member{
			UserId:   &commonv1.Id{Value: updated.UserID},
			Role:     updated.Role,
			JoinedAt: timestamppb.New(updated.JoinedAt),
		},
	}), nil
}

// --- authorization helpers ---

// checkMembership loads a member or returns an error.
func (s *Service) checkMembership(ctx context.Context, roomID, userID string) (domain.Member, error) {
	var m domain.Member
	err := s.readTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var ferr error
		m, ferr = s.members.Get(ctx, tx, roomID, userID)
		return ferr
	})
	return m, err
}

// requireRoomRole verifies the caller has at least minRole in the room.
// Admins bypass.
func (s *Service) requireRoomRole(ctx context.Context, tx pgx.Tx, roomID string, subject requestSubject, minRole commonv1.RoomRole) error {
	if subject.SystemRole >= commonv1.SystemRole_SYSTEM_ROLE_ADMINISTRATOR {
		return nil
	}
	member, err := s.members.Get(ctx, tx, roomID, subject.UserID)
	if err != nil {
		if isNotFound(err) {
			return vberr.PermissionDenied("manage_room", "room:"+roomID)
		}
		return err
	}
	if member.Role < minRole {
		return vberr.PermissionDenied("manage_room", "room:"+roomID)
	}
	return nil
}

// (Compile-time interface assertion lives in lifecycle.go where roomv1connect
// is imported.)
