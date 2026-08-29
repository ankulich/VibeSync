package app

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "vibesync/gen/go/vibesync/auth/v1"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	vberr "vibesync/libs/errors"
)

// ListLinkedProviders returns the streaming providers (Spotify, YouTube)
// linked to the user's account. Used by the profile page to show connection
// status. The caller must be the user themselves or an admin.
func (s *Service) ListLinkedProviders(
	ctx context.Context,
	req *connect.Request[authv1.ListLinkedProvidersRequest],
) (*connect.Response[authv1.ListLinkedProvidersResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	userID := req.Msg.GetUserId().GetValue()
	if userID == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.auth", "MISSING_USER_ID", "user_id required")
	}

	// Authorization: the caller must be the user or an admin.
	subject := subjectFromHeader(req.Header())
	if subject.UserID != userID && subject.SystemRole < commonv1.SystemRole_SYSTEM_ROLE_ADMINISTRATOR {
		return nil, vberr.PermissionDenied("list_providers", "user:"+userID)
	}

	var providers []*authv1.LinkedProvider
	err := s.readTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		accounts, ferr := s.accounts.ListByUser(ctx, tx, userID)
		if ferr != nil {
			return ferr
		}
		for _, a := range accounts {
			providers = append(providers, &authv1.LinkedProvider{
				Provider: a.Provider,
				LinkedAt: timestamppb.New(a.CreatedAt),
			})
		}
		return nil
	})
	if err != nil {
		return nil, vberr.Internal("LIST_PROVIDERS_FAILED", err.Error()).WithCause(err)
	}

	return connect.NewResponse(&authv1.ListLinkedProvidersResponse{
		Providers: providers,
	}), nil
}

// errorsAs is a thin alias to std errors.As (avoids repeated imports across
// the app package files).
func errorsAs(err error, target any) bool { return errors.As(err, target) }

// requestSubject carries the caller's identity from Connect headers (set by
// the API Gateway). Same pattern as the user/room/sync services.
type requestSubject struct {
	UserID     string
	SystemRole commonv1.SystemRole
}

// subjectFromHeader extracts the subject from X-Vibesync-User-Id and
// X-Vibesync-System-Role headers.
func subjectFromHeader(h http.Header) requestSubject {
	return requestSubject{
		UserID:     h.Get("X-Vibesync-User-Id"),
		SystemRole: parseSystemRole(h.Get("X-Vibesync-System-Role")),
	}
}

// parseSystemRole maps the header string to the enum.
func parseSystemRole(s string) commonv1.SystemRole {
	if s == "" {
		return commonv1.SystemRole_SYSTEM_ROLE_UNSPECIFIED
	}
	if v, ok := commonv1.SystemRole_value[s]; ok {
		return commonv1.SystemRole(v)
	}
	return commonv1.SystemRole_SYSTEM_ROLE_UNSPECIFIED
}
