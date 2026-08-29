package app

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"vibesync/apps/user-service/internal/domain"
	"vibesync/apps/user-service/internal/ports"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	userv1 "vibesync/gen/go/vibesync/user/v1"
	vberr "vibesync/libs/errors"

	userv1connect "vibesync/gen/go/vibesync/user/v1/userv1connect"
)

// GetUser loads a single user by ID. No authorization check: any caller may
// fetch a user's public profile (needed for room member display, etc.).
func (s *Service) GetUser(ctx context.Context, req *connect.Request[userv1.GetUserRequest]) (*connect.Response[userv1.GetUserResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	id := req.Msg.GetId().GetValue()
	if id == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.user", "MISSING_ID", "user id required")
	}

	var user domain.User
	err := s.readTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var ferr error
		user, ferr = s.users.GetByID(ctx, tx, id)
		return ferr
	})
	if err != nil {
		if isNotFound(err) {
			return nil, vberr.NotFound("user", id)
		}
		return nil, vberr.Internal("GET_USER_FAILED", err.Error()).WithCause(err)
	}
	return connect.NewResponse(&userv1.GetUserResponse{
		User: userToProto(user),
	}), nil
}

// UpdateUser updates the mutable fields (display_name, avatar_url) on the read
// model directly. Authorization: self-service (caller's user_id == target) OR
// admin. The subject is extracted from Connect headers set by the API Gateway
// (X-Vibesync-User-Id, X-Vibesync-System-Role).
func (s *Service) UpdateUser(ctx context.Context, req *connect.Request[userv1.UpdateUserRequest]) (*connect.Response[userv1.UpdateUserResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	id := req.Msg.GetId().GetValue()
	if id == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.user", "MISSING_ID", "user id required")
	}

	// Authorization: self-service or admin.
	subject := subjectFromHeader(req.Header())
	if subject.UserID != id && subject.SystemRole < commonv1.SystemRole_SYSTEM_ROLE_ADMINISTRATOR {
		return nil, vberr.PermissionDenied("update_user", "user:"+id)
	}

	// Load the existing user (to preserve non-mutated fields).
	var user domain.User
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var ferr error
		user, ferr = s.users.GetByID(ctx, tx, id)
		if ferr != nil {
			return ferr
		}
		// Apply the update.
		user.ApplyUpdate(s.now(), req.Msg.DisplayName, req.Msg.AvatarUrl)
		return s.users.Update(ctx, tx, user)
	})
	if err != nil {
		if isNotFound(err) {
			return nil, vberr.NotFound("user", id)
		}
		return nil, vberr.Internal("UPDATE_USER_FAILED", err.Error()).WithCause(err)
	}
	return connect.NewResponse(&userv1.UpdateUserResponse{
		User: userToProto(user),
	}), nil
}

// ListUsers returns a page of users. Authorization: admin-only
// (rbac.ActionViewUsers). Cursor-based pagination; optional search on username.
func (s *Service) ListUsers(ctx context.Context, req *connect.Request[userv1.ListUsersRequest]) (*connect.Response[userv1.ListUsersResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}

	// Authorization: admin-only.
	subject := subjectFromHeader(req.Header())
	if subject.SystemRole < commonv1.SystemRole_SYSTEM_ROLE_ADMINISTRATOR {
		return nil, vberr.PermissionDenied("view_users", "all")
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

	var users []domain.User
	var nextCursor string
	err := s.readTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var ferr error
		users, nextCursor, ferr = s.users.List(ctx, tx, cursor, int(limit), search)
		return ferr
	})
	if err != nil {
		return nil, vberr.Internal("LIST_USERS_FAILED", err.Error()).WithCause(err)
	}

	protoUsers := make([]*userv1.User, 0, len(users))
	for _, u := range users {
		protoUsers = append(protoUsers, userToProto(u))
	}
	var total uint64
	if len(users) > 0 {
		total = uint64(len(users)) // best-effort; exact count omitted (expensive)
	}
	return connect.NewResponse(&userv1.ListUsersResponse{
		Users: protoUsers,
		Page: &commonv1.PageResponse{
			NextCursor: nextCursor,
			Total:      total,
		},
	}), nil
}

// userToProto converts a domain User to the generated proto User message.
func userToProto(u domain.User) *userv1.User {
	return &userv1.User{
		Id:          &commonv1.Id{Value: u.ID},
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Email:       u.Email,
		AvatarUrl:   u.AvatarURL,
		SystemRole:  u.SystemRole,
		CreatedAt:   timestamppb.New(u.CreatedAt),
		UpdatedAt:   timestamppb.New(u.UpdatedAt),
	}
}

// requestSubject carries the caller's identity, extracted from Connect headers
// set by the API Gateway.
type requestSubject struct {
	UserID     string
	SystemRole commonv1.SystemRole
}

// subjectFromHeader extracts the subject from the Connect request headers.
// The API Gateway (future) sets these after validating the access token. In
// Phase 5, if the headers are absent (direct call, no gateway), the subject is
// zero-valued — which means UpdateUser requires admin and ListUsers is denied.
// This is intentionally restrictive: a missing subject is treated as anonymous.
func subjectFromHeader(h http.Header) requestSubject {
	return requestSubject{
		UserID:     h.Get("X-Vibesync-User-Id"),
		SystemRole: parseSystemRole(h.Get("X-Vibesync-System-Role")),
	}
}

// parseSystemRole converts the header string to the enum. The header carries
// the proto enum name (e.g. "SYSTEM_ROLE_ADMINISTRATOR") or numeric value.
func parseSystemRole(s string) commonv1.SystemRole {
	if s == "" {
		return commonv1.SystemRole_SYSTEM_ROLE_UNSPECIFIED
	}
	if v, ok := commonv1.SystemRole_value[s]; ok {
		return commonv1.SystemRole(v)
	}
	return commonv1.SystemRole(commonv1.SystemRole_value[s])
}

// isNotFound reports whether err is a ports.ErrNotFound.
func isNotFound(err error) bool {
	return err != nil && errors.Is(err, ports.ErrNotFound)
}

// Compile-time assertion that *Service satisfies the full handler interface.
var _ userv1connect.UserServiceHandler = (*Service)(nil)
