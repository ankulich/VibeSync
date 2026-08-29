// Package events contains the Kafka producer + outbox relay for the Media
// Service. Mirrors the room-service pattern (ADR-0004).
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"

	"vibesync/apps/media-service/internal/infra/postgres"
	vbkafka "vibesync/libs/kafka"
	vboutbox "vibesync/libs/outbox"
)

// Producer wraps a kafka-go Writer and implements vbkafka.Producer.
type Producer struct {
	w *kafka.Writer
}

// NewProducer constructs a Producer that writes to the given Kafka brokers.
func NewProducer(brokers []string) *Producer {
	w := &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Balancer:               &kafka.Hash{},
		RequiredAcks:           kafka.RequireAll,
		AllowAutoTopicCreation: true,
		BatchTimeout:           10 * time.Millisecond,
	}
	return &Producer{w: w}
}

// Publish writes a single message to its topic.
func (p *Producer) Publish(ctx context.Context, msg vbkafka.Message) error {
	km := kafka.Message{
		Topic: msg.Topic,
		Key:   []byte(msg.Key),
		Value: msg.Value,
		Time:  msg.Timestamp,
	}
	for k, v := range msg.Headers {
		km.Headers = append(km.Headers, kafka.Header{Key: k, Value: []byte(v)})
	}
	if err := p.w.WriteMessages(ctx, km); err != nil {
		return fmt.Errorf("events: publish: %w", err)
	}
	return nil
}

// Close releases the underlying Kafka writer.
func (p *Producer) Close() error {
	if p.w != nil {
		return p.w.Close()
	}
	return nil
}

var _ vbkafka.Producer = (*Producer)(nil)

// Publisher adapts outbox.Event to kafka.Message.
type Publisher struct{ prod *Producer }

// NewPublisher wraps a Producer for outbox-to-Kafka publishing.
func NewPublisher(prod *Producer) *Publisher { return &Publisher{prod: prod} }

// Publish converts an outbox event to a Kafka message and writes it.
func (p *Publisher) Publish(ctx context.Context, event vboutbox.Event) error {
	key := event.Key
	if key == "" {
		key = event.AggregateID
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now()
	}
	return p.prod.Publish(ctx, vbkafka.Message{
		Topic:     event.Topic,
		Key:       []byte(key),
		Value:     event.Payload,
		Headers:   eventHeaders(event),
		Timestamp: event.OccurredAt,
	})
}

func eventHeaders(e vboutbox.Event) map[string]string {
	h := map[string]string{
		"schema-version": e.Version,
		"event-id":       e.ID,
		"aggregate-id":   e.AggregateID,
	}
	for k, v := range e.Headers {
		h[k] = v
	}
	return h
}

// PendingPublisher adapts Publisher to the relay's interface.
type PendingPublisher struct{ pub *Publisher }

// NewPendingPublisher wraps a Publisher to consume PendingEvent rows.
func NewPendingPublisher(pub *Publisher) *PendingPublisher { return &PendingPublisher{pub: pub} }

// PublishOne converts a PendingEvent row to an outbox event and publishes it.
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

// Pool is the relay's minimal pool surface.
type Pool interface {
	BeginTx(ctx context.Context) (pgx.Tx, error)
}

// Locker is the leader-election surface.
type Locker interface {
	Acquire(ctx context.Context, key string, ttl time.Duration) (token uint64, release func() error, err error)
}

// ErrNotLeader is returned when the lock is held elsewhere.
var ErrNotLeader = errors.New("relay: not leader")

// RelayOptions configures the relay.
type RelayOptions struct {
	BatchSize int
	PollEvery time.Duration
	LockTTL   time.Duration
}

// Relay drains the Media outbox to Kafka. Leader-elected via Redis.
type Relay struct {
	pool      Pool
	publisher *PendingPublisher
	locker    Locker
	lockKey   string
	lockTTL   time.Duration
	batchSize int
	pollEvery time.Duration
}

// NewRelay constructs a Relay, applying defaults to zero-valued options.
func NewRelay(pool Pool, publisher *PendingPublisher, locker Locker, opts RelayOptions) *Relay {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 100
	}
	if opts.PollEvery <= 0 {
		opts.PollEvery = time.Second
	}
	if opts.LockTTL <= 0 {
		opts.LockTTL = 30 * time.Second
	}
	return &Relay{
		pool: pool, publisher: publisher, locker: locker,
		lockKey: "media:outbox:relay", lockTTL: opts.LockTTL,
		batchSize: opts.BatchSize, pollEvery: opts.PollEvery,
	}
}

// Run is the leader-elected drain loop. It runs until ctx is cancelled,
// repeatedly acquiring the lock, draining a batch of outbox events to Kafka,
// and sleeping when there is nothing to publish or the lock is held elsewhere.
func (r *Relay) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, release, err := r.locker.Acquire(ctx, r.lockKey, r.lockTTL)
		if err != nil {
			if errors.Is(err, ErrNotLeader) {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(r.pollEvery):
				}
				continue
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(r.pollEvery):
			}
			continue
		}
		published, _ := r.drainOnce(ctx)
		_ = release()
		if published == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(r.pollEvery):
			}
		}
	}
}

func (r *Relay) drainOnce(ctx context.Context) (int, error) {
	tx, err := r.pool.BeginTx(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	pending, err := postgres.FetchUnpublished(ctx, tx, r.batchSize)
	if err != nil {
		return 0, err
	}
	if len(pending) == 0 {
		return 0, nil
	}
	for _, pe := range pending {
		if err := r.publisher.PublishOne(ctx, pe); err != nil {
			return 0, fmt.Errorf("relay: publish %s: %w", pe.ID, err)
		}
		if err := postgres.MarkPublished(ctx, tx, pe.ID, time.Now().UTC()); err != nil {
			return 0, fmt.Errorf("relay: mark %s: %w", pe.ID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(pending), nil
}

// RedisLocker implements Locker via Redis SET NX EX.
type RedisLocker struct{ rdb *redis.Client }

// NewRedisLocker builds a Locker backed by the given Redis client.
func NewRedisLocker(rdb *redis.Client) *RedisLocker { return &RedisLocker{rdb: rdb} }

// Acquire takes the lock via SET NX EX. On success it returns the token and a
// release function; ErrNotLeader is returned when the lock is already held.
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
		got, err := l.rdb.Get(ctx, key).Int64()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				return nil
			}
			return err
		}
		if uint64(got) != token {
			return nil
		}
		return l.rdb.Del(ctx, key).Err()
	}
	return token, release, nil
}

// json is referenced by PendingEvent's RawMessage field; keep the import live
// defensively.
var _ = json.Marshal
