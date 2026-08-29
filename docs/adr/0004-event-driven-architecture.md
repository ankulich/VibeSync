# ADR-0004: Event-Driven Architecture patterns

- **Status:** Accepted
- **Date:** 2026-07-26
- **Phase:** 1 (Architecture)

## Context

The prompt mandates Event-Driven Architecture with: Kafka as the event bus,
CQRS where beneficial, the Outbox Pattern, the Saga Pattern, idempotent
consumers, event versioning, a Dead Letter Queue, event replay, and
distributed tracing. These patterns interact and must be applied consistently
or they create more problems than they solve.

## Decision

We apply these patterns as follows, scoped to where they pay off:

### Outbox Pattern (universal for state-changing services)

Every service that writes domain state *and* publishes an event writes both
in the **same Postgres transaction** via the outbox table. A separate relay
process drains the table to Kafka. This is the only way to atomically update
state and publish an event without 2PC.

- Port: `libs/outbox.Writer`, `libs/outbox.Relay`.
- Implementation: ships with Phase 4 (Auth), reused by every producing service.

### Event versioning (in the topic name)

Topics are named `<aggregate>.<event>.v<N>` (e.g. `user.created.v1`). A
new schema version is a new topic, not an in-place mutation. Consumers opt
into specific versions. This is the only schema-evolution strategy that
allows true event replay without rewriting history.

### Idempotent consumers (universal)

Consumers deduplicate by the event's `ID` (a ULID) using a Redis set with a
TTL matching the message retention window. Re-delivery of an already-seen
event is a no-op. The `libs/kafka` package provides this as a Middleware.

### Dead Letter Queue (universal)

After N failed attempts (default 5 with exponential backoff), a message is
published to `<topic>.dlq` and the original offset is committed. DLQs are
monitored; sustained growth triggers an alert.

### Saga Pattern (cross-service workflows only)

Sagas are used **only** where a workflow spans multiple services and there is
no single owner — e.g. "provision a room and its initial media queue" might
touch Room, Media, and Sync. We use **orchestration** (a dedicated
saga service or the originating service driving the workflow) over
choreography, because choreography's implicit flows are hard to debug and
impossible to retract. Sagas emit compensating events on failure.

Not every cross-service interaction is a saga. Simple request/response RPC
(Auth→User for user lookup) is direct Connect-RPC, not an event.

### CQRS (only where read/write shapes diverge)

CQRS is applied selectively — e.g. the Analytics Service has a write path
(event ingest) and a separate read path (dashboard queries) against different
stores. Most services do not need it and would only add complexity.

### Event replay

Every event is written to Mongo (the immutable log) in addition to Kafka.
Replay = read from Mongo, re-publish to Kafka. This decouples replay
semantics from Kafka's retention window.

## Consequences

- **Pros:** exactly-once *effect* (via outbox + idempotent consumers);
  schema evolution without downtime; debuggable workflows (orchestration);
  replayable history.
- **Cons:** the outbox relay is a new failure mode (must be monitored);
  orchestration sagas add a coordinator component; Mongo disk usage for the
  event log grows monotonically (TTL collections mitigate).
- **Topic naming is a contract:** once consumers depend on
  `user.created.v1`, that topic cannot be renamed, only superseded.

## Alternatives considered

- **2PC (two-phase commit):** unavailable in practice across Kafka + Postgres.
  Rejected.
- **Choreography for all sagas:** debuggability cost too high. Rejected as
  the default; may be used case-by-case if a workflow is genuinely simple.
