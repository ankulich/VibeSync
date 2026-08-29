// Package events contains the Kafka consumer + producer + relay for the Sync
// Service. The Sync Service is both a consumer (room.created.v1 → init room
// state) and a producer (sync.updated.v1 → outbox relay).
package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"

	vbkafka "vibesync/libs/kafka"
	vboutbox "vibesync/libs/outbox"

	"vibesync/apps/sync-service/internal/infra/postgres"
)

// --- Producer ---

// Producer wraps a kafka-go Writer.
type Producer struct{ w *kafka.Writer }

// NewProducer constructs a Producer.
func NewProducer(brokers []string) *Producer {
	return &Producer{w: &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Balancer:               &kafka.Hash{},
		RequiredAcks:           kafka.RequireAll,
		AllowAutoTopicCreation: true,
		BatchTimeout:           10 * time.Millisecond,
	}}
}

// Publish sends one message synchronously.
func (p *Producer) Publish(ctx context.Context, msg vbkafka.Message) error {
	km := kafka.Message{Topic: msg.Topic, Key: []byte(msg.Key), Value: msg.Value, Time: msg.Timestamp}
	for k, v := range msg.Headers {
		km.Headers = append(km.Headers, kafka.Header{Key: k, Value: []byte(v)})
	}
	if err := p.w.WriteMessages(ctx, km); err != nil {
		return fmt.Errorf("events: publish: %w", err)
	}
	return nil
}

// Close releases the writer.
func (p *Producer) Close() error {
	if p.w != nil {
		return p.w.Close()
	}
	return nil
}

// --- Publisher (outbox.Event → kafka.Message adapter) ---

// Publisher adapts outbox.Event to the Producer.
type Publisher struct{ prod *Producer }

// NewPublisher wraps a Producer.
func NewPublisher(prod *Producer) *Publisher { return &Publisher{prod: prod} }

// Publish translates and forwards.
func (p *Publisher) Publish(ctx context.Context, event vboutbox.Event) error {
	key := event.Key
	if key == "" {
		key = event.AggregateID
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now()
	}
	return p.prod.Publish(ctx, vbkafka.Message{
		Topic: event.Topic, Key: []byte(key), Value: event.Payload,
		Headers: eventHeaders(event), Timestamp: event.OccurredAt,
	})
}

func eventHeaders(e vboutbox.Event) map[string]string {
	h := map[string]string{
		"schema-version": e.Version, "event-id": e.ID, "aggregate-id": e.AggregateID,
	}
	for k, v := range e.Headers {
		h[k] = v
	}
	return h
}

// --- PendingPublisher (relay adapter) ---

// PendingPublisher adapts Publisher for the relay.
type PendingPublisher struct{ pub *Publisher }

// NewPendingPublisher wraps a Publisher for relay use.
func NewPendingPublisher(pub *Publisher) *PendingPublisher { return &PendingPublisher{pub: pub} }

// PublishOne forwards a PendingEvent.
func (p *PendingPublisher) PublishOne(ctx context.Context, pe postgres.PendingEvent) error {
	headers := pe.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	return p.pub.Publish(ctx, vboutbox.Event{
		ID: pe.ID, AggregateID: pe.AggregateID, Topic: pe.Topic, Key: pe.Key,
		Payload: pe.Payload, Headers: headers, OccurredAt: pe.OccurredAt, Version: pe.Version,
	})
}

// --- Consumer ---

// Consumer wraps a kafka-go Reader for consumer-group consumption.
type Consumer struct {
	reader *kafka.Reader
	logger *slog.Logger
}

// ConsumerOptions configures the consumer.
type ConsumerOptions struct {
	Brokers  []string
	GroupID  string
	Topic    string
	MinBytes int
	MaxBytes int
}

// NewConsumer constructs a Consumer.
func NewConsumer(opts ConsumerOptions, logger *slog.Logger) *Consumer {
	if opts.MinBytes == 0 {
		opts.MinBytes = 1
	}
	if opts.MaxBytes == 0 {
		opts.MaxBytes = 1 << 20
	}
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: opts.Brokers, GroupID: opts.GroupID, Topic: opts.Topic,
		MinBytes: opts.MinBytes, MaxBytes: opts.MaxBytes,
		StartOffset: kafka.FirstOffset,
	})
	return &Consumer{reader: reader, logger: logger}
}

// Run reads messages and dispatches to handler. Blocks until ctx canceled.
func (c *Consumer) Run(ctx context.Context, handler vbkafka.Handler) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			c.logger.Error("consumer fetch error", "err", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		msg := vbkafka.Message{
			Topic: m.Topic, Partition: int32(m.Partition), Offset: m.Offset,
			Key: m.Key, Value: m.Value, Timestamp: m.Time,
		}
		msg.Headers = make(map[string]string, len(m.Headers))
		for _, h := range m.Headers {
			msg.Headers[h.Key] = string(h.Value)
		}
		if err := handler.Handle(ctx, msg); err != nil {
			c.logger.Error("consumer handler error, will retry", "err", err, "topic", msg.Topic)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(1 * time.Second):
			}
			continue
		}
		if err := c.reader.CommitMessages(ctx, m); err != nil {
			c.logger.Warn("consumer commit failed", "err", err)
		}
	}
}

// Close releases the reader.
func (c *Consumer) Close() error {
	if c.reader != nil {
		return c.reader.Close()
	}
	return nil
}

// --- Relay (outbox drain with Redis leader election) ---

// RelayPool is the relay's minimal pool surface.
type RelayPool interface {
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

// Relay drains the outbox to Kafka.
type Relay struct {
	pool      RelayPool
	publisher *PendingPublisher
	locker    Locker
	lockKey   string
	lockTTL   time.Duration
	batchSize int
	pollEvery time.Duration
}

// NewRelay constructs a Relay.
func NewRelay(pool RelayPool, publisher *PendingPublisher, locker Locker, opts RelayOptions) *Relay {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 100
	}
	if opts.PollEvery <= 0 {
		opts.PollEvery = time.Second
	}
	if opts.LockTTL <= 0 {
		opts.LockTTL = 30 * time.Second
	}
	return &Relay{pool: pool, publisher: publisher, locker: locker,
		lockKey: "sync:outbox:relay", lockTTL: opts.LockTTL,
		batchSize: opts.BatchSize, pollEvery: opts.PollEvery}
}

// Run drains the outbox until ctx canceled.
func (r *Relay) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, release, err := r.locker.Acquire(ctx, r.lockKey, r.lockTTL)
		if err != nil {
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

// NewRedisLocker constructs a RedisLocker.
func NewRedisLocker(rdb *redis.Client) *RedisLocker { return &RedisLocker{rdb: rdb} }

// Acquire attempts SET NX EX.
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

// Compile-time checks.
var (
	_ vbkafka.Producer = (*Producer)(nil)
	_ vbkafka.Consumer = (*Consumer)(nil)
)
