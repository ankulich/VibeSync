# ADR-0014: User Service owns profile updates

- **Status:** Accepted
- **Date:** 2026-08-04
- **Phase:** 5 (User Service)

## Context

Auth owns `auth.users` (Phase 4, ADR-0010) and is the system-of-record for
identity. The User Service consumes `user.created.v1` events to build a
read-optimized copy of user data (Phase 5). When a user updates their profile,
we need to decide where that write lands.

Three options were considered:

1. **User Service writes directly to its read model** for presentation fields.
2. **Route through Auth**, which emits `user.updated.v1` for the User Service
   to consume.
3. **Defer `UpdateUser` entirely** until a later phase.

## Decision

**The User Service owns profile updates (display_name, avatar_url) directly
against its read model.** Auth remains the system-of-record for identity fields
(email, username, system_role). The two stores intentionally diverge on
presentation fields.

### Why this was chosen

- **Simplest to ship now.** Presentation fields (`display_name`, `avatar_url`)
  are low-stakes and don't need cross-service consistency; identity fields stay
  authoritative at Auth.
- **Option 2 is overkill.** Routing through Auth adds a round-trip, a new proto
  RPC, and a new event for fields that don't warrant any of it.
- **Option 3 blocks a user-facing feature** with no offsetting benefit.

## Consequences

- **The two stores can diverge on `display_name`/`avatar_url`.** This is
  acceptable because those fields are presentation-only.
- **`UpdateUser` does NOT emit an event.** Updates are local to the User
  Service. If a future feature needs consistency (e.g. an audit trail of
  profile changes), a `user.updated.v1` event can be added without breaking
  this decision.
- **Identity fields remain Auth-owned.** Updates to `email`, `username`, or
  `system_role` are not in scope for the User Service's `UpdateUser`.

## Alternatives considered

- **Route through Auth (Option 2):** strongest consistency story, but the cost
  (new RPC + new event + round-trip) is unjustified for presentation-only
  fields. Reversible: an `user.updated.v1` event can be introduced later if
  needed. Rejected for now.
- **Defer `UpdateUser` (Option 3):** avoids the divergence entirely, but
  blocks a user-facing feature for a theoretical concern. Rejected.
