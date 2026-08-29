// Package events contains the Kafka producer + outbox relay for the Room
// Service. Mirrors the auth-service pattern (ADR-0004).
package events

import (
	"context"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"

	vbkafka "vibesync/libs/kafka"
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
