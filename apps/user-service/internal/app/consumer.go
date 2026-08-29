package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	vbkafka "vibesync/libs/kafka"

	"vibesync/apps/user-service/internal/domain"
	"vibesync/apps/user-service/internal/ports"
)

// TxRunner runs a function inside a Postgres transaction, committing on nil
// return and rolling back on error. The consumer handler accepts this rather
// than the full ports.Pool so it's trivially testable (tests pass a function
// that calls the fake repo directly, no pgx.Tx mock needed).
type TxRunner func(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error

// UserCreatedHandler is the Kafka handler for `user.created.v1` events. It
// deserializes the event payload and projects the user into the read model
// via UserRepo.Upsert (idempotent by construction: INSERT ... ON CONFLICT).
//
// The handler is wrapped by the idempotency middleware in main.go, so exact
// redeliveries are short-circuited before reaching Handle. But Handle is ALSO
// idempotent on its own (the Upsert), so even a middleware bypass (Redis down)
// is safe — defense in depth per ADR-0015.
type UserCreatedHandler struct {
	users  ports.UserRepo
	runTx  TxRunner
	logger *slog.Logger
}

// NewUserCreatedHandler constructs the handler. runTx wraps the pool's
// transaction lifecycle (typically Service.withTx or a direct closure over
// pool.BeginTx).
func NewUserCreatedHandler(users ports.UserRepo, runTx TxRunner, logger *slog.Logger) *UserCreatedHandler {
	return &UserCreatedHandler{users: users, runTx: runTx, logger: logger}
}

// Handle deserializes the event and projects the user. Returns nil on success
// (commits the Kafka offset) or an error (the consumer retries).
func (h *UserCreatedHandler) Handle(ctx context.Context, msg vbkafka.Message) error {
	var event domain.UserCreatedV1
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		// Malformed payload: log and commit (don't block the partition on a
		// poison message). The DLQ pattern (future) would route this to a
		// dead-letter topic instead.
		h.logger.Error("consumer: malformed user.created.v1 payload, skipping",
			"err", err, "topic", msg.Topic, "offset", msg.Offset)
		return nil
	}
	user, err := domain.ProjectFromEvent(event, time.Now().UTC())
	if err != nil {
		h.logger.Error("consumer: invalid user.created.v1 event, skipping",
			"err", err, "topic", msg.Topic, "user_id", event.UserID)
		return nil
	}

	// Project in a transaction. Upsert is idempotent so redeliveries are safe.
	err = h.runTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return h.users.Upsert(ctx, tx, user)
	})
	if err != nil {
		h.logger.Error("consumer: upsert failed",
			"err", err, "user_id", user.ID)
		return err // will retry
	}
	h.logger.Info("consumer: projected user",
		"user_id", user.ID, "email", user.Email)
	return nil
}

// Compile-time check that UserCreatedHandler satisfies kafka.Handler.
var _ vbkafka.Handler = (*UserCreatedHandler)(nil)
