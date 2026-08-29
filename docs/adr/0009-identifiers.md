# ADR-0009: Identifier strategy

- **Status:** Accepted
- **Date:** 2026-07-26
- **Phase:** 1 (Architecture)

## Context

Every primary key in the system needs an identifier type. The choice has
long-term consequences for index performance, sortability, debuggability, and
operational ergonomics.

## Decision

We use **ULIDs** (Universally Unique Lexicographically Sortable Identifiers)
for all primary keys, via `libs/id` (wrapping `github.com/oklog/ulid/v2`).

Rationale:

- **128-bit, collision-free** without a central issuer or coordination —
  suitable for distributed generation across services.
- **Time-ordered** (48-bit ms timestamp prefix). This keeps b-tree indexes
  balanced under append-heavy workloads (rooms, events, messages) — sequential
  inserts are dramatically cheaper than random ones (UUIDv4).
- **Canonical 26-char Crockford base32** form is URL-safe and sorts as a
  byte string — convenient for cursor-based pagination (no separate
  `created_at` index needed for ordering).
- **80 bits of entropy per millisecond** is far beyond the birthday-bound
  collision risk for any realistic insert rate.

### Concurrency safety

`libs/id` uses a single locked monotonic entropy source so concurrent
callers never collide within the same millisecond (the underlying `ulid`
package guarantees monotonicity only within a single goroutine otherwise).

### Wire representation

On the wire, IDs travel as the canonical 26-char string inside
`vibesync.common.v1.Id`. We deliberately do not use bytes on the wire: the
string form is human-readable in logs and JSON, and the overhead is
negligible at our ID volumes.

### Kafka partitioning

When an event must preserve per-aggregate ordering, the Kafka message key is
the aggregate's ULID. Time-ordering of the ULID also gives a stable
partition assignment.

## Consequences

- **Pros:** no central ID service; indexes stay healthy; sortable; debuggable.
- **Cons:** 26 chars vs 16 bytes (UUID) — slightly larger on disk and wire.
  Acceptable.
- **Validation:** `libs/id.Parse` enforces canonical form (rejects ambiguous
  `I`/`L`/`O`/`U` and lowercase) to catch corruption early.

## Alternatives considered

- **UUIDv4:** random, index-hostile at scale. Rejected.
- **UUIDv7:** similar properties to ULID; less ecosystem traction in Go when
  this ADR was written. Reconsider if a standard library type lands.
- **Integer sequences (SERIAL):** requires a central issuer (the database)
  and couples services to it. Rejected for cross-service IDs.
