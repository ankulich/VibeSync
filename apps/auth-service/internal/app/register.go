package app

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"vibesync/apps/auth-service/internal/domain"
	authv1 "vibesync/gen/go/vibesync/auth/v1"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	vberr "vibesync/libs/errors"
	vboutbox "vibesync/libs/outbox"
)

// Register creates a new user account with email + password and returns a
// token pair (same shape as Login). The user is created in the Active state
// with the default USER role, and a user.created.v1 event is staged in the
// outbox so the User Service read model stays consistent.
//
// Failure modes:
//   - invalid email/username/display_name → InvalidArgument (from domain validation)
//   - email already registered             → AlreadyExists
//   - username taken                       → AlreadyExists (DB unique constraint)
//   - weak/empty password                  → InvalidArgument
func (s *Service) Register(ctx context.Context, req *connect.Request[authv1.RegisterRequest]) (*connect.Response[authv1.RegisterResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	email := req.Msg.GetEmail()
	username := req.Msg.GetUsername()
	password := req.Msg.GetPassword()
	if email == "" || username == "" || password == "" {
		return nil, vberr.InvalidArgumentFor("vibesync.auth", "MISSING_FIELDS", "email, username, and password are required")
	}
	if len(password) < 8 {
		return nil, vberr.InvalidArgumentFor("vibesync.auth", "PASSWORD_TOO_SHORT", "password must be at least 8 characters")
	}

	session, material, err := s.registerUseCase(ctx, email, username, password,
		req.Msg.GetDisplayName(), req.Msg.GetDeviceLabel())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&authv1.RegisterResponse{
		Session: &authv1.AuthSession{
			Tokens: &authv1.TokenPair{
				AccessToken:  material.AccessToken,
				RefreshToken: material.RefreshToken,
				TokenType:    "Bearer",
				ExpiresIn:    material.ExpiresIn,
			},
			UserId: &commonv1.Id{Value: session.UserID},
		},
	}), nil
}

// registerUseCase: validate → hash password → check duplicate → create user +
// outbox event → issue session. All writes in one tx.
func (s *Service) registerUseCase(ctx context.Context, email, username, password, displayName, deviceLabel string) (domain.Session, tokenMaterial, error) {
	var (
		session  domain.Session
		material tokenMaterial
	)
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		now := s.now()

		// 1. Domain validation (email format, username format, display name).
		user, err := domain.NewUser(now, domain.NewUserParams{
			Email:       email,
			Username:    username,
			DisplayName: displayName,
		})
		if err != nil {
			return vberr.InvalidArgumentFor("vibesync.auth", "INVALID_FIELDS", err.Error()).WithCause(err)
		}

		// 2. Hash the password (argon2id).
		hash, err := s.hasher.Hash(password)
		if err != nil {
			return vberr.Internal("PASSWORD_HASH_FAILED", err.Error()).WithCause(err)
		}
		user.SetPassword(hash)

		// 3. Duplicate check: a pre-check for a clean error + the DB unique
		// constraint as the race-safe backstop.
		if _, err := s.users.GetByEmail(ctx, tx, user.Email); err == nil {
			return vberr.AlreadyExists("user", user.Email)
		} else if !isNotFound(err) {
			return err
		}

		// 4. Assign ID and create.
		user.ID = s.idgen.New()
		if err := s.users.Create(ctx, tx, user); err != nil {
			if isUniqueViolation(err) {
				return vberr.AlreadyExists("user", user.Email)
			}
			return err
		}

		// 5. Stage user.created.v1 (same as the OAuth path) so the User
		// Service read model picks the user up.
		payload, _ := json.Marshal(map[string]any{
			"user_id":    user.ID,
			"email":      user.Email,
			"username":   user.Username,
			"provider":   "password",
			"created_at": now.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
		if err := s.outbox.Append(ctx, tx, vboutbox.Event{
			ID:          s.idgen.New(),
			AggregateID: user.ID,
			Topic:       "user.created.v1",
			Key:         user.ID,
			Payload:     payload,
			OccurredAt:  now,
			Version:     "v1",
		}); err != nil {
			return err
		}

		// 6. Issue the session + tokens (shared with Login/OAuth).
		return s.issueSessionForUser(ctx, tx, user, deviceLabel, &session, &material)
	})
	return session, material, err
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errorsAs(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
