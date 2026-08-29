# ADR-0010: Auth Service owns canonical user records

- **Status:** Accepted
- **Date:** 2026-07-27
- **Phase:** 4 (Auth Service)

## Context

The `user.proto` contract has no `CreateUser` RPC and no lookup-by-email, and we
do not want to add one in Phase 4. But `CompleteOAuth` must upsert a user on the
first OAuth login — it needs somewhere to persist the user record and resolve an
existing user by provider subject or email.

We have to pick where the canonical user record lives in the meantime. Three
options were on the table:

1. Auth owns `auth.users` and creates/looks-up users directly.
2. Add `CreateUser` / `GetUserByEmail` RPCs to `user.proto` now, backed by a
   stub User Service that we would have to stand up early.
3. Auth owns only auth-only state (sessions, tokens) and defers the user
   profile entirely until Phase 5.

## Decision

The **Auth Service owns the canonical `auth.users` table** and creates and
looks up users directly. The User Service (Phase 5) will build its read model
from the `user.created.v1` event that Auth emits via the outbox — it will not
own its own writable users table.

### Key choices

- **Auth is the system of record for users until Phase 5.** It writes the row,
  assigns the ULID, and emits `user.created.v1` through the transactional
  outbox so the event lands exactly once with the row commit.
- **Username uniqueness lives in `auth.users`.** No separate registry; the
  unique constraint is on the auth table.
- **Phase 5 consumes, not duplicates.** The User Service is a read model
  populated from `user.created.v1` (and later `user.updated.*`). Decoupling is
  via the event contract, not a shared table or a synchronous RPC.

### Why chosen

- **Simplest path to a working OAuth flow today.** No stub service, no contract
  churn, no cross-service call on the login hot path.
- **`user.created.v1` gives clean decoupling for Phase 5.** The event is the
  integration point; the User Service never needs to know how Auth persists.
- **Single source of truth at Auth** keeps the data duplication risk contained
  until Phase 5 exists.

## Consequences

- **Pros:** ships OAuth in Phase 4 with no extra service; event contract is the
  stable integration seam; one writer for users avoids split-brain.
- **Cons:** some data duplication between `auth.users` and the future User
  Service read model until Phase 5 is live. Mitigated by Auth being the only
  writer.
- **Trade-off accepted:** Auth carries user profile concerns briefly. Phase 5
  takes over the read side via events; ownership of writes can be revisited
  then.

## Alternatives considered

- **Add `CreateUser` / `GetUserByEmail` to `user.proto` now:** would force a
  stub User Service into Phase 4 and add a synchronous cross-service call on
  every OAuth login. Rejected for schedule and coupling reasons.
- **Auth owns auth-only state, defer user profile:** blocks a working OAuth
  flow — `CompleteOAuth` has nowhere to put the user. Rejected.
