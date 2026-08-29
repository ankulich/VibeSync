package events

import (
	"context"
	"time"

	vbkafka "vibesync/libs/kafka"
	vboutbox "vibesync/libs/outbox"
)

// Publisher adapts an outbox.Publisher (which takes outbox.Event) onto a
// kafka.Producer (which takes kafka.Message). The relay calls this; the
// translation is mechanical: Event.Payload is already bytes (the use case
// serialized the event body), and Event.Key defaults to AggregateID for
// per-aggregate partition ordering.
type Publisher struct {
	producer vbkafka.Producer
}

// NewPublisher wraps a kafka.Producer as an outbox.Publisher.
func NewPublisher(producer vbkafka.Producer) *Publisher { return &Publisher{producer: producer} }

// Publish translates and forwards to the underlying producer.
func (p *Publisher) Publish(ctx context.Context, event vboutbox.Event) error {
	key := event.Key
	if key == "" {
		key = event.AggregateID
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now()
	}
	return p.producer.Publish(ctx, vbkafka.Message{
		Topic:     event.Topic,
		Key:       []byte(key),
		Value:     payloadBytes(event),
		Headers:   eventHeaders(event),
		Timestamp: event.OccurredAt,
	})
}

// payloadBytes ensures the Message.Value is real bytes. The outbox stores
// payload as JSON-encoded []byte in the use case; if a caller passed a raw
// []byte via Event.Payload it travels through unchanged. If they passed a
// JSON-encoded map[string]any (unusual), we re-encode to canonical bytes.
func payloadBytes(e vboutbox.Event) []byte {
	// Event.Payload is []byte already by type; nothing to do. The use case
	// is responsible for having serialized the event body. We do NOT re-marshal
	// here because that would double-encode a []byte as a JSON string.
	return e.Payload
}

// eventHeaders injects schema-version and tracing metadata. Tracing headers
// are populated by the relay from the active span (when wired in main.go).
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

// Compile-time interface check.
var _ vboutbox.Publisher = (*Publisher)(nil)
