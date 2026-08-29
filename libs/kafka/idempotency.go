// Idempotency middleware for Kafka consumers. See ADR-0015.
//
// At-least-once delivery guarantees that a consumer will see the same message
// more than once under normal operation (broker restart, consumer rebalance,
// network blip). Handlers must therefore be idempotent. This middleware
// provides a cheap, reusable dedupe layer backed by Redis, keyed on the
// event ID carried in the message headers.
//
// The middleware is intentionally non-authoritative: a handler should STILL
// be idempotent on its own (e.g. INSERT ... ON CONFLICT DO NOTHING). The
// middleware just avoids the cost of re-executing the handler body for the
// common case of a quick redelivery. If Redis is unavailable, the middleware
// fails open (calls the handler), trusting the handler's own idempotency.

package kafka

import (
	"context"
	"errors"
	"time"
)

// KV is the narrow key/value surface the idempotency middleware needs. The
// production implementation is backed by Redis (via libs/redislib or the
// service's own redis client); tests pass a fake.
//
// The interface is deliberately smaller than redislib.KV so that any KV store
// (even an in-memory map) can satisfy it. SetNX is "set if not exists" — the
// atomic primitive the middleware uses to mark an event as seen without races.
type KV interface {
	// SetNX sets key=value with ttl, but only if key does not already exist.
	// Returns true if the key was set (i.e., this is the first sighting),
	// false if it already existed. The production Redis implementation uses
	// SET NX EX under the hood.
	SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error)
}

// ErrNoEventID is returned when a message has no event ID in its headers and
// no usable key. The middleware cannot dedupe without an identifier, so it
// logs a warning and calls the handler (no dedupe) rather than dropping the
// message.
var ErrNoEventID = errors.New("kafka: no event id for idempotency")

// HeaderEventID is the standard header carrying the event's unique ID. The
// Auth Service outbox relay sets this when publishing (see infra/events/
// publisher.go: eventHeaders injects "event-id"). Consumers that read events
// produced by other services should agree on this header name.
const HeaderEventID = "event-id"

// IdempotencyMiddleware wraps a Handler so that repeated delivery of the same
// event (by event ID) is short-circuited. The first sighting marks the ID in
// kv with the given ttl; subsequent sightings within the ttl return nil
// immediately (the handler is not called, the offset commits).
//
// ttl should match the Kafka topic's retention window (default 24h) so the
// dedupe set does not grow unbounded. A shorter ttl risks re-processing
// events after it expires; a longer ttl wastes Redis memory.
//
// If the message has no event-id header and no key, the middleware calls the
// handler without dedupe (fail-open) — better to process twice than to drop.
func IdempotencyMiddleware(kv KV, ttl time.Duration) Middleware {
	return func(next Handler) Handler {
		return HandlerFunc(func(ctx context.Context, msg Message) error {
			id := eventID(msg)
			if id == "" {
				// No way to dedupe; let the handler's own idempotency handle it.
				return next.Handle(ctx, msg)
			}
			key := idempotencyKey(msg.Topic, id)
			first, err := kv.SetNX(ctx, key, "1", ttl)
			if err != nil {
				// KV backend unavailable: fail open. The handler must be
				// idempotent on its own (defense in depth).
				return next.Handle(ctx, msg)
			}
			if !first {
				// Already seen: short-circuit. Returning nil commits the
				// offset without invoking the handler.
				return nil
			}
			return next.Handle(ctx, msg)
		})
	}
}

// eventID extracts the dedupe key from a message. Prefers the event-id header
// (set by the outbox relay); falls back to the message key (which is the
// aggregate ID — less precise but still dedupes exact redeliveries of the
// same message). Returns "" if neither is usable.
func eventID(msg Message) string {
	if id := msg.Headers[HeaderEventID]; id != "" {
		return id
	}
	return string(msg.Key)
}

// idempotencyKey builds the Redis key for a (topic, eventID) pair. Namespaced
// by topic so events with the same ID across topics (unlikely but possible)
// don't collide.
func idempotencyKey(topic, eventID string) string {
	return "idem:" + topic + ":" + eventID
}
