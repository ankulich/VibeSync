package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	vboutbox "vibesync/libs/outbox"
)

// OutboxWriter is the Postgres-backed outbox.Writer for Auth. It implements
// ports.OutboxWriter (which takes pgx.Tx) — the canonical pattern every
// service reproduces: stage events in the same tx as the domain write, so
// the commit is atomic and the relay can drain to Kafka asynchronously.
//
// The underlying libs/outbox.Writer interface took an outbox.Tx; we deliberately
// implement against pgx.Tx here because that's what the use case holds, and
// avoiding a second adapter layer keeps the hot path simple. The port
// (ports.OutboxWriter) is the contract the use case depends on.
type OutboxWriter struct{}

// NewOutboxWriter returns an OutboxWriter. Stateless.
func NewOutboxWriter() *OutboxWriter { return &OutboxWriter{} }

// Append inserts one row per event into auth.outbox, all within tx. The
// payload and headers are JSON-encoded (JSONB columns). On any failure the
// caller's tx is expected to roll back, abandoning the staged rows along
// with the domain write — atomic by construction.
func (OutboxWriter) Append(ctx context.Context, tx pgx.Tx, events ...vboutbox.Event) error {
	for i, e := range events {
		if e.Topic == "" {
			return fmt.Errorf("outbox: event %d missing topic", i)
		}
		payload, err := json.Marshal(e.Payload)
		if err != nil {
			return fmt.Errorf("outbox: marshal payload %d: %w", i, err)
		}
		headers, err := json.Marshal(e.Headers)
		if err != nil {
			return fmt.Errorf("outbox: marshal headers %d: %w", i, err)
		}
		key := e.Key
		if key == "" {
			key = e.AggregateID
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO auth.outbox
			    (id, aggregate_id, topic, key, payload, headers, occurred_at, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			e.ID, e.AggregateID, e.Topic, key, payload, headers, e.OccurredAt, e.Version,
		)
		if err != nil {
			return fmt.Errorf("outbox: insert event %d: %w", i, err)
		}
	}
	return nil
}

// PendingEvent is an unpublished outbox row fetched by the relay. The payload
// travels as raw JSON bytes (the use case's serialized event body).
type PendingEvent struct {
	ID          string
	AggregateID string
	Topic       string
	Key         string
	Payload     json.RawMessage
	Headers     map[string]string
	OccurredAt  time.Time
	Version     string
}

// FetchUnpublished is exposed for the relay adapter (infra/events/relay.go)
// rather than going through the ports interface — it's an infra-internal
// concern, not something the use case calls.
func FetchUnpublished(ctx context.Context, tx pgx.Tx, limit int) ([]PendingEvent, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, aggregate_id, topic, key, payload, headers, occurred_at, version
		  FROM auth.outbox
		 WHERE published = FALSE
		 ORDER BY occurred_at
		 LIMIT $1
		 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, fmt.Errorf("outbox: fetch unpublished: %w", err)
	}
	defer rows.Close()
	var out []PendingEvent
	for rows.Next() {
		var pe PendingEvent
		var headers json.RawMessage
		if err := rows.Scan(&pe.ID, &pe.AggregateID, &pe.Topic, &pe.Key,
			&pe.Payload, &headers, &pe.OccurredAt, &pe.Version); err != nil {
			return nil, fmt.Errorf("outbox: scan: %w", err)
		}
		if len(headers) > 0 {
			_ = json.Unmarshal(headers, &pe.Headers) // best-effort; empty map on error
		}
		out = append(out, pe)
	}
	return out, rows.Err()
}

// MarkPublished flags a row as published. Called by the relay after Kafka acks.
func MarkPublished(ctx context.Context, tx pgx.Tx, id string, at time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth.outbox
		   SET published = TRUE, published_at = $2
		 WHERE id = $1`, id, at)
	if err != nil {
		return fmt.Errorf("outbox: mark published: %w", err)
	}
	return nil
}
