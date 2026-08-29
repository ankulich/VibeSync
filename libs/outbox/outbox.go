// Package outbox defines the Transactional Outbox port used by every service
// that must atomically write domain state AND publish an event.
//
// Problem solved: a service that updates Postgres and then publishes to Kafka
// cannot do both atomically — either order can fail, leaving the system
// inconsistent. The outbox writes events into the same DB transaction as the
// domain write; a separate relay drains the table to Kafka and marks rows
// sent. See ADR (Event-Driven Architecture).
//
// This package defines the contract. The Postgres-backed implementation
// ships with the first service that needs it (Phase 4, Auth). Tests will use
// the in-memory implementation provided here.
package outbox

import (
	"context"
	"time"
)

// Event is a single outbox row, decoupled from any specific transport.
type Event struct {
	// ID is a deterministic ULID used as the Kafka message key when
	// partitioning by aggregate; empty for fan-out events.
	ID string
	// AggregateID is the entity this event concerns (room id, user id, ...).
	// Routes the event to the correct Kafka partition for ordering.
	AggregateID string
	// Topic is the destination Kafka topic, e.g. "user.created.v1".
	Topic string
	// Key is the Kafka partitioning key. Defaults to AggregateID.
	Key string
	// Payload is the serialized event body (JSON or protobuf).
	Payload []byte
	// Headers carry tracing and schema metadata.
	Headers map[string]string
	// OccurredAt is when the domain event was generated.
	OccurredAt time.Time
	// Version is the event schema version (the v1 in user.created.v1).
	Version string
}

// Writer appends events to the outbox. Implementations MUST guarantee that
// events appended in the same DB transaction as the domain write are either
// both committed or both rolled back.
type Writer interface {
	// Append adds events to the outbox bound to tx. The caller owns the
	// transaction lifecycle; Append only stages rows within it.
	Append(ctx context.Context, tx Tx, events ...Event) error
}

// Tx is the minimal transaction surface the outbox needs. Services alias
// their own tx type (pgx.Tx, sql.Tx) to this interface.
type Tx interface {
	// Exec is the single-method surface; the outbox only inserts rows.
	Exec(ctx context.Context, sql string, args ...any) (Rows, error)
}

// Rows abstracts the (rows-affected, error) tuple across drivers.
type Rows struct {
	Affected int64
}

// Relay drains the outbox table and publishes to Kafka. A single instance per
// service process; multiple instances coordinate via an advisory lock or a
// leader-election key (Redis). Implementation lands in Phase 4.
type Relay interface {
	// Run blocks until ctx is canceled, polling the outbox and publishing.
	Run(ctx context.Context) error
}

// Publisher is the Kafka-facing surface the Relay uses. The Kafka lib
// (libs/kafka) implements this.
type Publisher interface {
	Publish(ctx context.Context, event Event) error
}
