package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"vibesync/apps/auth-service/internal/infra/postgres"
	vboutbox "vibesync/libs/outbox"
)

// PendingPublisher adapts *Publisher to the relay's PublisherAdapter surface.
// It rebuilds an outbox.Event from each PendingEvent row and forwards it to
// the publisher, which performs the Event → kafka.Message translation and
// injects the standard headers (event-id, schema-version).
type PendingPublisher struct {
	pub *Publisher
}

// NewPendingPublisher wraps a *Publisher for relay use.
func NewPendingPublisher(pub *Publisher) *PendingPublisher { return &PendingPublisher{pub: pub} }

// PublishOne rebuilds an outbox.Event from the PendingEvent row and publishes
// it. The payload bytes (the use case's serialized event body) travel
// unchanged as the Kafka message value.
func (p *PendingPublisher) PublishOne(ctx context.Context, pe postgres.PendingEvent) error {
	headers := pe.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	return p.pub.Publish(ctx, vboutbox.Event{
		ID:          pe.ID,
		AggregateID: pe.AggregateID,
		Topic:       pe.Topic,
		Key:         pe.Key,
		Payload:     pe.Payload,
		Headers:     headers,
		OccurredAt:  pe.OccurredAt,
		Version:     pe.Version,
	})
}

// RedisLocker is the Locker implementation backed by Redis SET NX EX. Acquire
// returns a fencing token and a release function that DELs only if the value
// matches.
//
// Phase 4 simplification: release uses GET-then-DEL rather than a Lua CAS.
// A strict CAS is the textbook correct version; the simple form here is
// bounded by the TTL and is fine for single-instance dev and the documented
// N-instance correctness tradeoff. Upgrading to EVAL-based CAS is a future
// hardening step, not a Phase 4 blocker.
type RedisLocker struct {
	rdb *redis.Client
}

// NewRedisLocker constructs a RedisLocker.
func NewRedisLocker(rdb *redis.Client) *RedisLocker { return &RedisLocker{rdb: rdb} }

// Acquire attempts SET NX EX. Returns ErrNotLeader if the key is held.
func (l *RedisLocker) Acquire(ctx context.Context, key string, ttl time.Duration) (uint64, func() error, error) {
	token := uint64(time.Now().UnixNano())
	ok, err := l.rdb.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return 0, nil, fmt.Errorf("redis_locker: setnx: %w", err)
	}
	if !ok {
		return 0, nil, ErrNotLeader
	}
	release := func() error {
		// CAS-ish: only delete if we still hold it. A full Lua CAS is the
		// strict version; the simple GET compare-and-DEL here is a documented
		// Phase 4 simplification. Wrong-direction races are bounded by the TTL.
		got, err := l.rdb.Get(ctx, key).Int64()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				return nil // already gone; nothing to release
			}
			return err
		}
		if uint64(got) != token {
			return nil // lease expired and was re-acquired; not ours
		}
		return l.rdb.Del(ctx, key).Err()
	}
	return token, release, nil
}

// Compile-time interface checks.
var (
	_ Locker           = (*RedisLocker)(nil)
	_ PublisherAdapter = (*PendingPublisher)(nil)
)
