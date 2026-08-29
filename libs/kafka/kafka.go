// Package kafka defines VibeSync's Kafka producer/consumer contract.
//
// Implementation lands with the first service that produces events (Phase 4,
// Auth, which emits user.created.v1). Until then this package pins the surface
// so the outbox relay and consumer wiring can be coded against a stable type.
//
// Design notes (ADR Event-Driven Architecture):
//   - Producers are idempotent: the Relay passes an event ID that Kafka
//     uses for exactly-once delivery via enable.idempotence=true.
//   - Consumers dedupe via an idempotency key (event.ID) stored in Redis;
//     libs/kafka provides a Middleware for this.
//   - Dead Letter Queue: messages failing N times move to <topic>.dlq.
//   - Schema versioning: topics end with .vN; consumers opt in per version.
package kafka

import (
	"context"
	"time"
)

// Message is the Kafka wire shape, decoupled from the Sarama/Confluent type.
type Message struct {
	Topic     string
	Partition int32
	Offset    int64
	Key       []byte
	Value     []byte
	Headers   map[string]string
	Timestamp time.Time
}

// Producer publishes messages. Implementations wrap segmentio/kafka-go or
// confluent-kafka-go; chosen in Phase 4.
type Producer interface {
	// Publish is synchronous: it blocks until the broker acks. The Relay
	// calls this serially per partition to preserve ordering.
	Publish(ctx context.Context, msg Message) error
	// Close releases the underlying connection pool.
	Close() error
}

// Handler processes a single message. Returning an error schedules a retry;
// returning nil commits the offset.
//
// Handlers MUST be idempotent. The kafka package supplies an Idempotency
// Middleware that short-circips redeliveries of the same event.ID.
type Handler interface {
	Handle(ctx context.Context, msg Message) error
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, msg Message) error

// Handle implements Handler.
func (f HandlerFunc) Handle(ctx context.Context, msg Message) error { return f(ctx, msg) }

// Consumer subscribes to one or more topics and dispatches to a Handler.
type Consumer interface {
	// Run blocks until ctx is canceled. It commits offsets only after the
	// Handler returns nil, providing at-least-once delivery.
	Run(ctx context.Context, handler Handler) error
	// Close releases the consumer group.
	Close() error
}

// Middleware wraps a Handler for cross-cutting concerns (idempotency,
// tracing, metrics, retry-to-DLQ).
type Middleware func(Handler) Handler

// Chain composes middlewares right-to-left: Chain(a, b)(h) = a(b(h)).
func Chain(mw ...Middleware) Middleware {
	return func(h Handler) Handler {
		for i := len(mw) - 1; i >= 0; i-- {
			h = mw[i](h)
		}
		return h
	}
}
