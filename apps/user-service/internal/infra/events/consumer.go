// Package events contains the Kafka consumer adapter for the User Service.
// This is the repo's first consumer implementation and establishes the pattern
// every future consumer follows.
//
// Uses segmentio/kafka-go's Reader configured with a consumer GroupID. Kafka
// consumer groups handle partition assignment and offset tracking — no leader
// election needed (unlike the producer-side relay). Run commits offsets only
// after the Handler returns nil (at-least-once delivery).
package events

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/segmentio/kafka-go"

	vbkafka "vibesync/libs/kafka"
)

// Consumer wraps a kafka-go Reader and implements vbkafka.Consumer.
type Consumer struct {
	reader *kafka.Reader
	logger *slog.Logger
}

// Options configures the consumer.
type Options struct {
	Brokers  []string
	GroupID  string // consumer group; partitions are assigned within the group
	Topic    string
	MinBytes int // fetch min (default 1 = return as soon as available)
	MaxBytes int // fetch max (default 1 MB)
}

// NewConsumer constructs a Consumer. The reader is configured for consumer
// groups: GroupID enables offset tracking on the broker, so a restart resumes
// from the last committed offset and a rebalance redistributes partitions.
func NewConsumer(opts Options, logger *slog.Logger) *Consumer {
	if opts.MinBytes == 0 {
		opts.MinBytes = 1
	}
	if opts.MaxBytes == 0 {
		opts.MaxBytes = 1 << 20 // 1 MB
	}
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  opts.Brokers,
		GroupID:  opts.GroupID,
		Topic:    opts.Topic,
		MinBytes: opts.MinBytes,
		MaxBytes: opts.MaxBytes,
		// StartOffset only applies when the group has no committed offset yet.
		// FirstOffset = process from the beginning so the read model is
		// complete on first deploy; subsequent restarts resume from committed.
		StartOffset: kafka.FirstOffset,
	})
	return &Consumer{reader: reader, logger: logger}
}

// Run reads messages in a loop, dispatching each to handler. Offsets are
// committed only after the handler returns nil — at-least-once semantics.
// On handler error the message is not committed and will be redelivered; Run
// backs off briefly to avoid a hot retry loop. Blocks until ctx is canceled.
func (c *Consumer) Run(ctx context.Context, handler vbkafka.Handler) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// FetchMessage does not auto-commit; we CommitMessages explicitly
		// after successful handling. A crash before commit means the message
		// is redelivered.
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			c.logger.Error("consumer fetch error", "err", err, "topic", c.reader.Config().Topic)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(shortBackoff):
			}
			continue
		}
		msg := vbkafkaMessageFromKafkaGo(m)
		if err := handler.Handle(ctx, msg); err != nil {
			c.logger.Error("consumer handler error, will retry",
				"err", err, "topic", msg.Topic, "partition", m.Partition, "offset", m.Offset)
			// Do NOT commit. The next FetchMessage will return the same
			// message. Back off to avoid hammering a failing handler.
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(retryBackoff):
			}
			continue
		}
		// Handler succeeded: commit the offset so the group advances.
		if err := c.reader.CommitMessages(ctx, m); err != nil {
			// Commit failed (broker blip). The message will be redelivered; the
			// handler is idempotent (via the idempotency middleware + upsert),
			// so a redelivery is safe. Log and continue.
			c.logger.Warn("consumer commit failed; message may be redelivered",
				"err", err, "topic", msg.Topic, "offset", m.Offset)
		}
	}
}

// Close releases the reader. Safe to call after Run returns.
func (c *Consumer) Close() error {
	if c.reader != nil {
		return c.reader.Close()
	}
	return nil
}

const (
	shortBackoff = 100 * time.Millisecond
	retryBackoff = 1 * time.Second
)

// vbkafkaMessageFromKafkaGo converts a kafka-go Message to the wire-decoupled
// vbkafka.Message. Headers are flattened to map[string]string.
func vbkafkaMessageFromKafkaGo(m kafka.Message) vbkafka.Message {
	headers := make(map[string]string, len(m.Headers))
	for _, h := range m.Headers {
		headers[h.Key] = string(h.Value)
	}
	return vbkafka.Message{
		Topic:     m.Topic,
		Partition: int32(m.Partition),
		Offset:    m.Offset,
		Key:       m.Key,
		Value:     m.Value,
		Headers:   headers,
		Timestamp: m.Time,
	}
}

// Compile-time interface check.
var _ vbkafka.Consumer = (*Consumer)(nil)
