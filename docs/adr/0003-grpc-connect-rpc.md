# ADR-0003: Connect-RPC as the API transport

- **Status:** Accepted
- **Date:** 2026-07-26
- **Phase:** 1 (Architecture)

## Context

The prompt mandates gRPC, REST (`/api/v1`), WebSocket, and WebRTC. A naive
reading implies four separate transport stacks, each with their own schema,
serialization, and auth handling — significant surface area and a strong
incentive for drift between the "gRPC API" and the "REST API."

## Decision

We adopt **Connect-RPC** (buf.build/connect-go) as the primary RPC transport.
From a single `.proto` schema, Connect simultaneously serves:

- **gRPC-over-H2** (binary protobuf) — for service-to-service and native
  clients.
- **HTTP/JSON** (Connect's `connectrpc.com/connect` protocol) — for the
  browser frontend, which can `fetch()` the same endpoints without a gRPC
  proxy.

This collapses "gRPC" and "REST" into one schema-driven surface. Where
protocols genuinely require HTTP (OAuth callbacks, webhook signatures,
well-known URLs like `/.well-known/jwks.json`), we use Gin (see `libs/web`).

### Role of the other transports

- **WebSocket:** reserved for the Sync Service's low-latency streaming in
  Phase 7. The Subscribe stream may be exposed over WebSocket in addition to
  gRPC streaming for browser clients that prefer it.
- **WebRTC:** peer-to-peer media (Phase 9, Media Service). Coturn provides
  TURN/STUN. Unrelated to the RPC layer.

## Consequences

- **Pros:** one schema, one set of types, one auth interceptor; HTTP/JSON for
  browsers is free; gRPC tooling (grpcurl, Postman) works against the same
  endpoints.
- **Cons:** some gRPC-specific tooling expects classic grpc-go reflection;
  Connect has its own reflection handler (`connectrpc.com/reflection`) which
  we'll add in the relevant phase.
- **REST `/api/v1`:** the Connect HTTP/JSON endpoints live at
  `/<package>.<Service>/<Method>` (e.g. `/vibesync.auth.v1.AuthService/Login`).
  This is *not* the literal `/api/v1/...` path the prompt mentions; we treat
  the prompt's `/api/v1` as the conceptual "versioned public API" and map it
  to the Connect paths. If a stricter REST surface is ever needed, a thin
  gateway can translate.

## Alternatives considered

- **Classic gRPC + grpc-gateway:** doubles the schema surface (one .proto
  plus HTTP annotations) and requires a separate gateway process. Rejected.
- **Pure REST/OpenAPI:** loses streaming (needed for Sync) and strong typing
  across services. Rejected.
