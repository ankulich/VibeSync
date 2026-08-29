# ADR-0001: Workspace layout and module boundaries

- **Status:** Accepted
- **Date:** 2026-07-26
- **Phase:** 1 (Architecture)

## Context

VibeSync will eventually consist of 11 microservices (Auth, User, Room, Sync,
Playback, Media, Provider, Notification, Analytics, Storage, plus an API
Gateway), a shared library layer, and a frontend. We need a repository
structure that:

1. Lets each service be built, tested, versioned, and deployed independently.
2. Shares common code (errors, logging, telemetry, ports) without coupling
   services to each other's internals.
3. Supports the API-first mandate: contracts (`.proto`) are the source of
   truth and must generate code in a stable import path.
4. Works on developer laptops (Windows, macOS, Linux) and in CI without
   bespoke setup.

The Go workspace (`go.work`) feature, stable since Go 1.18, is the natural
fit, but the granularity of modules within it is the decision.

## Decision

We use **one Go module per service and one per the shared library layer**,
bound together by `go.work`:

```
go.work
tools/      — private toolchain module (buf, protoc-gen-*, golangci-lint)
gen/go/     — generated protobuf code (single module)
libs/       — shared libraries (single module, many packages)
apps/<svc>/  — one module per microservice
proto/      — canonical .proto sources (not a module)
```

### Key choices

- **No root `go.mod`.** A root module would conflict with `go.work` semantics
  and force every subdirectory into one versioning unit. Anti-pattern.
- **One `libs` module, not one per library.** Shared libraries are internal
  and never published independently; per-lib modules would add ~15 `go.mod`
  files with no benefit. *(This is a refinement of the original plan, which
  proposed per-lib modules. Reversible if external consumers ever need it.)*
- **Services are per-module.** Each `apps/<svc>` is its own module so it can
  be tagged and released independently, and so a change to one service's
  dependencies cannot break another's build.
- **`tools/` is intentionally NOT a workspace member.** It pins the
  toolchain (buf, golangci-lint) via `go tool` directives and is invoked with
  `GOWORK=off`. Keeping it out of the workspace prevents transitive
  dependency conflicts between the linter's graph and the application's.

## Consequences

- **Pros:** clean module boundaries; services version independently; shared
  code has one canonical home; `go build ./gen/go/... ./libs/...` works.
- **Cons:** adding a new service requires `go work use ./apps/<new>` (one
  line); `go build ./...` from the repo root does *not* work because there is
  no root module — use `make build` or build modules explicitly.
- **Trade-off accepted:** the small ceremony of `go work use` is worth the
  isolation it buys us.

## Alternatives considered

- **Single monolithic module:** simpler, but couples all services to one
  dependency set and one release tag. Rejected.
- **Per-library modules in `libs/`:** maximum isolation, but our libraries
  are never published separately and the maintenance cost (~15 go.mod files
  to keep in sync) outweighs the benefit. Rejected.
