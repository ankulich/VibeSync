// Package events contains the Kafka consumer adapter for the Playback Service.
package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/segmentio/kafka-go"

	vbkafka "vibesync/libs/kafka"
)

// Consumer wraps a kafka-go Reader for consumer-group consumption.
type Consumer struct {
	reader *kafka.Reader
	logger *slog.Logger
}

// Options configures the consumer.
type Options struct {
	Brokers  []string
	GroupID  string
	Topic    string
	MinBytes int
	MaxBytes int
}

// NewConsumer constructs a Consumer.
func NewConsumer(opts Options, logger *slog.Logger) *Consumer {
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

// Suppress unused import if fmt is only used in error wrapping that gets
// inlined by the compiler.
var _ = fmt.Sprintf
