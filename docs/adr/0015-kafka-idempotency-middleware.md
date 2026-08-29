# ADR-0015: Kafka idempotency middleware

- **Status:** Accepted
- **Date:** 2026-08-04
- **Phase:** 5 (User Service)

## Context

Kafka gives us at-least-once delivery, which means duplicates are normal — a
broker restart or a consumer rebalance will redeliver messages. Every consumer
in the repo needs a way to absorb those duplicates without double-applying
state changes. Rather than have each handler re-solve this, we want one
reusable dedupe layer.

## Decision

**A Redis-backed idempotency middleware lives in `libs/kafka`, deduplicating
messages by event ID. Every consumer in the repo reuses it.** The middleware
takes a narrow `KV` interface (SetNX only) so it is testable without Redis and
backend-agnostic.

### How it works

- **First sighting of an event ID:** the middleware marks it in Redis with a
  TTL (default 24h, matching Kafka retention).
- **Repeat sighting:** returns `nil` immediately — the handler is not called,
  and the offset commits.
- **Redis unavailable:** fails open (calls the handler), trusting the
  handler's own idempotency.
- **Event ID source:** the `event-id` message header (set by the outbox
  relay), falling back to the message key if the header is absent.

### Why

- **Duplicates are routine, not exceptional.** The middleware provides a cheap,
  reusable dedupe layer so handlers don't each pay the cost.
- **Defense in depth.** Handlers should STILL be idempotent on their own
  (e.g. `INSERT ... ON CONFLICT`); the middleware is a first line, not a
  substitute.

### Why in `libs/kafka` (not per-service)

- **The `libs/kafka` doc comment already promised it.** Centralizing makes the
  promise true.
- **Every consumer gets it for free** — no per-service wiring or drift.
- **The `KV` interface keeps it backend-agnostic**, so the middleware is not
  welded to Redis for testing or future swaps.

## Consequences

- **One Redis dependency for consumers**, which is already present for other
  uses — no new infra.
- **The middleware is non-authoritative.** Because it fails open when Redis is
  down, handlers must still be idempotent on their own.
- **A handler error after `SetNX` means a redelivery is deduped.** The handler
  must be able to tolerate this (it already is, if idempotent) — acceptable
  trade-off for the simplicity of not rolling back the dedupe mark on handler
  failure.

## Alternatives considered

- **Per-service idempotency:** maximum locality, but every consumer
  re-implements the same dedupe logic and the `libs/kafka` contract goes
  unfulfilled. Rejected.
- **Strict (fail-closed) Redis requirement:** stronger dedupe guarantees, but
  it couples message processing to Redis availability and contradicts the
  at-least-once + idempotent-handler model. Rejected.
