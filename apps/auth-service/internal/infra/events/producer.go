// Package events contains the Kafka-facing adapters for Auth: the producer
// that publishes to Kafka, the publisher that adapts outbox.Event to the
// kafka.Message shape, and the relay that drains the outbox table.
//
// The relay coordinates with other Auth instances via a Redis lock so only one
// instance publishes at a time (avoids duplicate delivery from N replicas
// each polling the same outbox). See ADR-0004 (Event-Driven).
package events

import (
	"context"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"

	vbkafka "vibesync/libs/kafka"
)

// Producer is the kafka.Producer implementation using segmentio/kafka-go.
// Writes are synchronous (the relay calls Publish serially per partition to
// preserve ordering within an aggregate).
type Producer struct {
	w *kafka.Writer
}

// NewProducer constructs a Producer. brokers is the bootstrap list; topicBase
// is the prefix used by all topics (events specify their own full topic name
// in outbox.Event.Topic, which overrides this writer's default).
func NewProducer(brokers []string) *Producer {
	w := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Balancer:     &kafka.Hash{}, // partition by message key for per-aggregate ordering
		RequiredAcks: kafka.RequireAll,
		// Idempotent producer: Kafka dedupes by (PID, sequence number) so
		// retries on network blips do not duplicate. kafka-go enables
		// idempotency automatically when RequiredAcks = RequireAll.
		AllowAutoTopicCreation: true,
		BatchTimeout:           10 * time.Millisecond,
	}
	return &Producer{w: w}
}

// Publish sends one message. Blocks until the broker acks.
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

// Close releases the underlying writer.
func (p *Producer) Close() error {
	if p.w != nil {
		return p.w.Close()
	}
	return nil
}

// Compile-time interface check.
var _ vbkafka.Producer = (*Producer)(nil)
