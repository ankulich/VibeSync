package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/user-service/internal/domain"
	"vibesync/apps/user-service/internal/ports"
	vbkafka "vibesync/libs/kafka"
)

// fakeRepo is an in-memory UserRepo for consumer handler tests.
type fakeRepo struct {
	mu    sync.Mutex
	users map[string]domain.User
	err   error // inject an error to test retry behavior
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{users: make(map[string]domain.User)}
}

func (f *fakeRepo) Upsert(_ context.Context, _ pgx.Tx, u domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.users[u.ID] = u
	return nil
}

func (f *fakeRepo) GetByID(_ context.Context, _ pgx.Tx, id string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return domain.User{}, ports.NotFound("user", id)
	}
	return u, nil
}

func (f *fakeRepo) Update(_ context.Context, _ pgx.Tx, u domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[u.ID] = u
	return nil
}

func (f *fakeRepo) List(_ context.Context, _ pgx.Tx, _ string, _ int, _ string) ([]domain.User, string, error) {
	return nil, "", nil
}

// fakeTxRunner is a TxRunner that calls fn with a nil tx (the fake repo
// ignores the tx arg). This sidesteps the large pgx.Tx interface entirely.
func fakeTxRunner() TxRunner {
	return func(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
		return fn(ctx, nil)
	}
}

func TestUserCreatedHandlerProjectsValidEvent(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	handler := NewUserCreatedHandler(repo, fakeTxRunner(), slog.Default())

	payload, _ := json.Marshal(domain.UserCreatedV1{
		UserID:   "01J6USER000000000000000A",
		Email:    "alice@example.com",
		Username: "alice",
		Provider: "spotify",
	})
	msg := vbkafka.Message{
		Topic:   "user.created.v1",
		Value:   payload,
		Headers: map[string]string{"event-id": "evt_1"},
	}
	if err := handler.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	u, ok := repo.users["01J6USER000000000000000A"]
	if !ok {
		t.Fatal("user was not projected into the repo")
	}
	if u.Email != "alice@example.com" {
		t.Errorf("Email = %q", u.Email)
	}
}

func TestUserCreatedHandlerSkipsMalformedPayload(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	handler := NewUserCreatedHandler(repo, fakeTxRunner(), slog.Default())

	msg := vbkafka.Message{
		Topic: "user.created.v1",
		Value: []byte("{not json"),
	}
	if err := handler.Handle(context.Background(), msg); err != nil {
		t.Errorf("malformed payload should return nil (skip), got %v", err)
	}
	if len(repo.users) != 0 {
		t.Error("no users should be projected from a malformed payload")
	}
}

func TestUserCreatedHandlerSkipsInvalidEvent(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	handler := NewUserCreatedHandler(repo, fakeTxRunner(), slog.Default())

	payload, _ := json.Marshal(map[string]string{"user_id": ""})
	msg := vbkafka.Message{Topic: "user.created.v1", Value: payload}
	if err := handler.Handle(context.Background(), msg); err != nil {
		t.Errorf("invalid event should return nil (skip), got %v", err)
	}
	if len(repo.users) != 0 {
		t.Error("no users should be projected from an invalid event")
	}
}

func TestUserCreatedHandlerRetriesOnUpsertError(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	repo.err = errors.New("db down")
	handler := NewUserCreatedHandler(repo, fakeTxRunner(), slog.Default())

	payload, _ := json.Marshal(domain.UserCreatedV1{
		UserID: "u1", Email: "a@b.co", Username: "a",
	})
	msg := vbkafka.Message{Topic: "user.created.v1", Value: payload}
	err := handler.Handle(context.Background(), msg)
	if err == nil {
		t.Fatal("upsert error should propagate (trigger retry)")
	}
}

func TestUserCreatedHandlerIdempotentRedelivery(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	handler := NewUserCreatedHandler(repo, fakeTxRunner(), slog.Default())

	payload, _ := json.Marshal(domain.UserCreatedV1{
		UserID:   "01J6USER000000000000000A",
		Email:    "alice@example.com",
		Username: "alice",
	})
	msg := vbkafka.Message{
		Topic:   "user.created.v1",
		Value:   payload,
		Headers: map[string]string{"event-id": "evt_1"},
	}
	// Deliver the same message twice (simulating redelivery).
	_ = handler.Handle(context.Background(), msg)
	_ = handler.Handle(context.Background(), msg)
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.users) != 1 {
		t.Errorf("redelivery should result in 1 user; got %d", len(repo.users))
	}
}

// Ensure time is referenced (used by the handler's time.Now().UTC() call; this
// var suppresses the unused-import check if the test file's time usage is
// optimized away).
var _ = time.Now
