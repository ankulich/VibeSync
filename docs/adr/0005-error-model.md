# ADR-0005: Typed domain error model

- **Status:** Accepted
- **Date:** 2026-07-26
- **Phase:** 1 (Architecture)

## Context

Service code needs to return errors that:

1. Carry a stable machine-readable code so clients can branch on them.
2. Map cleanly to gRPC/Connect status codes.
3. Carry structured debugging metadata without leaking internals to clients.
4. Compose via `errors.Is` / `errors.As` so wrapping and classification work.

Go's `error` interface alone is too loose; `fmt.Errorf` with `%w` loses the
structure. We need a canonical type that every service uses.

## Decision

The `libs/errors` package defines `*errors.Error` with fields:

- `Kind` → maps 1:1 to a Connect/gRPC code.
- `Reason` → stable SCREAMING_SNAKE string surfaced to clients (e.g.
  `ROOM_FULL`, `STALE_FENCING_TOKEN`).
- `Domain` → namespaces reasons (e.g. `vibesync.room`).
- `Message` → human-readable description.
- `Meta` → structured key/value debugging data.
- `Cause` → wrapped error, preserved through `errors.Is/As`.

Typed constructors (`NotFound`, `AlreadyExists`, `InvalidArgument`,
`PermissionDenied`, `Unauthenticated`, `FailedPrecondition`, `Conflict`,
`ResourceExhausted`, `Internal`) fix the `Kind` so call sites cannot
misclassify.

### Transport translation

`errors.ToConnect(err)` converts any error to a `*connect.Error` with the
correct code; `errors.FromConnect(err)` reverses it on the client side. The
RPC interceptor chain (`libs/rpc`) applies this at handler returns, so
service code returns `*Error` and never imports Connect.

## Consequences

- **Pros:** call sites read naturally (`return errors.NotFound("room", id)`);
  clients branch on `Reason`; debug metadata travels without leaking.
- **Cons:** one more type to learn; the `Kind` → code mapping is fixed (a
  new code requires a library change).
- **Internal messages vs. client messages:** `Message` may contain
  operational detail; the transport layer does *not* strip it currently. If
  we later need to hide internals, a sanitizer at the boundary can redact
  based on `Kind == Internal`.

## Alternatives considered

- **Bare `fmt.Errorf`:** loses structure. Rejected.
- **`google.rpc.Status` directly at call sites:** verbose and leaks protobuf
  types into domain code. Rejected; we keep the protobuf representation at
  the transport boundary only.
- **Sentinel errors (`errors.Is(err, ErrRoomFull)`):** doesn't carry
  per-instance data (the room id). Rejected as the primary pattern; used
  where appropriate as `*Error` values compared via `Reason`.
