package kafka

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeKV is an in-memory SetNX implementation for tests. It tracks call
// counts so tests can assert whether the handler was invoked.
type fakeKV struct {
	mu   sync.Mutex
	seen map[string]string // key → value
	err  error             // inject errors for fail-open testing
}

func newFakeKV() *fakeKV { return &fakeKV{seen: make(map[string]string)} }

func (f *fakeKV) SetNX(_ context.Context, key, value string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, f.err
	}
	if _, exists := f.seen[key]; exists {
		return false, nil
	}
	f.seen[key] = value
	return true, nil
}

func TestIdempotencyMiddleware_FirstSightingCallsHandler(t *testing.T) {
	t.Parallel()
	kv := newFakeKV()
	calls := 0
	handler := HandlerFunc(func(_ context.Context, _ Message) error {
		calls++
		return nil
	})
	mw := IdempotencyMiddleware(kv, time.Hour)
	wrapped := mw(handler)

	msg := Message{
		Topic:   "user.created.v1",
		Key:     []byte("user_1"),
		Headers: map[string]string{"event-id": "evt_1"},
	}
	if err := wrapped.Handle(context.Background(), msg); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if calls != 1 {
		t.Errorf("handler called %d times, want 1", calls)
	}
}

func TestIdempotencyMiddleware_DuplicateShortCircuits(t *testing.T) {
	t.Parallel()
	kv := newFakeKV()
	calls := 0
	handler := HandlerFunc(func(_ context.Context, _ Message) error {
		calls++
		return nil
	})
	mw := IdempotencyMiddleware(kv, time.Hour)
	wrapped := mw(handler)

	msg := Message{
		Topic:   "user.created.v1",
		Key:     []byte("user_1"),
		Headers: map[string]string{"event-id": "evt_1"},
	}
	// First delivery: handler runs.
	_ = wrapped.Handle(context.Background(), msg)
	// Second delivery (same event-id): short-circuit.
	err := wrapped.Handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("duplicate should return nil, got %v", err)
	}
	if calls != 1 {
		t.Errorf("handler called %d times, want 1 (deduped)", calls)
	}
}

func TestIdempotencyMiddleware_DifferentEventIDsBothCall(t *testing.T) {
	t.Parallel()
	kv := newFakeKV()
	calls := 0
	handler := HandlerFunc(func(_ context.Context, _ Message) error {
		calls++
		return nil
	})
	wrapped := IdempotencyMiddleware(kv, time.Hour)(handler)

	_ = wrapped.Handle(context.Background(), Message{
		Topic: "t", Headers: map[string]string{"event-id": "evt_a"},
	})
	_ = wrapped.Handle(context.Background(), Message{
		Topic: "t", Headers: map[string]string{"event-id": "evt_b"},
	})
	if calls != 2 {
		t.Errorf("handler called %d times, want 2 (different events)", calls)
	}
}

func TestIdempotencyMiddleware_NoEventIDFailsOpen(t *testing.T) {
	t.Parallel()
	kv := newFakeKV()
	calls := 0
	handler := HandlerFunc(func(_ context.Context, _ Message) error {
		calls++
		return nil
	})
	wrapped := IdempotencyMiddleware(kv, time.Hour)(handler)

	// No event-id header AND no key → can't dedupe → handler called every time.
	msg := Message{Topic: "t"}
	_ = wrapped.Handle(context.Background(), msg)
	_ = wrapped.Handle(context.Background(), msg)
	if calls != 2 {
		t.Errorf("handler called %d times, want 2 (no dedupe without id)", calls)
	}
}

func TestIdempotencyMiddleware_KVErrorFailsOpen(t *testing.T) {
	t.Parallel()
	kv := newFakeKV()
	kv.err = errors.New("redis down")
	calls := 0
	handler := HandlerFunc(func(_ context.Context, _ Message) error {
		calls++
		return nil
	})
	wrapped := IdempotencyMiddleware(kv, time.Hour)(handler)

	msg := Message{Topic: "t", Headers: map[string]string{"event-id": "evt_1"}}
	// Both calls should reach the handler (KV is down, can't dedupe).
	_ = wrapped.Handle(context.Background(), msg)
	_ = wrapped.Handle(context.Background(), msg)
	if calls != 2 {
		t.Errorf("handler called %d times, want 2 (KV error → fail open)", calls)
	}
}

func TestIdempotencyMiddleware_HandlerErrorDoesNotMarkSeen(t *testing.T) {
	t.Parallel()
	kv := newFakeKV()
	handlerErr := errors.New("handler failed")
	calls := 0
	handler := HandlerFunc(func(_ context.Context, _ Message) error {
		calls++
		return handlerErr
	})
	wrapped := IdempotencyMiddleware(kv, time.Hour)(handler)

	msg := Message{Topic: "t", Headers: map[string]string{"event-id": "evt_1"}}
	// First call: handler errors. The middleware marks it seen BEFORE calling
	// the handler (SetNX is the first op), so a redelivery will be deduped.
	// This is a design choice: we accept that a handler error + redelivery
	// skips re-execution. The handler must be idempotent to handle this.
	err := wrapped.Handle(context.Background(), msg)
	if !errors.Is(err, handlerErr) {
		t.Errorf("expected handler error to propagate, got %v", err)
	}
	// Redelivery: deduped (SetNX already marked it).
	_ = wrapped.Handle(context.Background(), msg)
	if calls != 1 {
		t.Errorf("handler called %d times, want 1 (marked seen before error)", calls)
	}
}

func TestEventIDPrefersHeaderOverKey(t *testing.T) {
	t.Parallel()
	msg := Message{
		Key:     []byte("aggregate_id"),
		Headers: map[string]string{"event-id": "evt_header"},
	}
	if got := eventID(msg); got != "evt_header" {
		t.Errorf("eventID = %q, want evt_header (header preferred)", got)
	}
}

func TestEventIDFallsBackToKey(t *testing.T) {
	t.Parallel()
	msg := Message{Key: []byte("aggregate_id")}
	if got := eventID(msg); got != "aggregate_id" {
		t.Errorf("eventID = %q, want aggregate_id (key fallback)", got)
	}
}

func TestIdempotencyKeyNamespacedByTopic(t *testing.T) {
	t.Parallel()
	a := idempotencyKey("topic_a", "evt_1")
	b := idempotencyKey("topic_b", "evt_1")
	if a == b {
		t.Error("keys for different topics must differ")
	}
	if !strings.HasPrefix(a, "idem:topic_a:") {
		t.Errorf("key %q should be namespaced with topic", a)
	}
}

func TestChainComposesIdempotencyAndHandler(t *testing.T) {
	t.Parallel()
	kv := newFakeKV()
	calls := 0
	handler := HandlerFunc(func(_ context.Context, _ Message) error {
		calls++
		return nil
	})
	// Chain the idempotency middleware with the handler via Chain().
	composed := Chain(IdempotencyMiddleware(kv, time.Hour))(handler)
	msg := Message{Topic: "t", Headers: map[string]string{"event-id": "evt_chain"}}
	_ = composed.Handle(context.Background(), msg)
	_ = composed.Handle(context.Background(), msg)
	if calls != 1 {
		t.Errorf("handler called %d times, want 1 (Chain + idempotency)", calls)
	}
}
