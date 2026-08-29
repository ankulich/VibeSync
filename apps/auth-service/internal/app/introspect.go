package app

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "vibesync/gen/go/vibesync/auth/v1"
	authv1connect "vibesync/gen/go/vibesync/auth/v1/authv1connect"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

// Introspect validates an access token and returns its claims. Used by the API
// Gateway and other services that trust the Auth Service's JWKS but want a
// definitive active/inactive answer (e.g. for high-value operations, or when
// they don't want to fetch + cache JWKS themselves).
//
// Returns active=false (not an error) for invalid/expired tokens, matching the
// OAuth2 RFC 7662 introspection semantics. Distinguish this from a *service*
// error (DB down, signer broken) which returns a real error.
func (s *Service) Introspect(ctx context.Context, req *connect.Request[authv1.IntrospectRequest]) (*connect.Response[authv1.IntrospectResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	token := req.Msg.GetAccessToken()
	if token == "" {
		return connect.NewResponse(&authv1.IntrospectResponse{Active: false}), nil
	}

	claims, err := s.signer.Verify(ctx, token)
	if err != nil {
		// Verify returns *errors.Error with Kind=Unauthenticated for any token
		// problem. Map to active=false per RFC 7662.
		if isAuthErr(err) {
			return connect.NewResponse(&authv1.IntrospectResponse{Active: false}), nil
		}
		return nil, err
	}
	return connect.NewResponse(&authv1.IntrospectResponse{
		Active:     true,
		UserId:     &commonv1.Id{Value: claims.Subject},
		SystemRole: commonv1.SystemRole(claims.SystemRole),
		ExpiresAt:  timestamppb.New(claims.ExpiresAt),
		Scope:      claims.Scope,
	}), nil
}

// isAuthErr reports whether err is one of our Unauthenticated errors. Verify
// returns *errors.Error with Kind=Unauthenticated; we check the Code() to
// avoid importing the vberrors Kind enum here.
func isAuthErr(err error) bool {
	type coder interface{ Code() uint32 }
	ce, ok := err.(coder)
	if !ok {
		return false
	}
	// connect.CodeUnauthenticated == 16.
	return ce.Code() == 16
}

// Compile-time assertion that *Service satisfies the full handler interface.
// If a method is missing or has the wrong signature, this line fails the
// build with a pointer at the missing method.
var _ authv1connect.AuthServiceHandler = (*Service)(nil)
