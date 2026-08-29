# ADR-0002: Protobuf toolchain via buf and `go tool`

- **Status:** Accepted
- **Date:** 2026-07-26
- **Phase:** 1 (Architecture)

## Context

VibeSync is API-first: every service's contract is a `.proto` file, and the
generated Go code is consumed by services and the frontend. We need a
reproducible, version-pinned way to:

1. Lint `.proto` files for API hygiene (naming, comments, breaking changes).
2. Generate Go code (messages + Connect-RPC services).
3. Detect breaking contract changes in CI.

`protoc` + hand-rolled plugin invocation is fragile. The **buf** tool is the
industry standard for this, but the development machines do not have `buf`
or `protoc` installed globally — and we explicitly do not want to require
global installs (reproducibility, onboarding friction).

## Decision

We use **buf** (v2 config schema) with **local plugins**, where "local"
means: the plugin binaries are built from versions pinned in
`tools/go.mod` via Go 1.24+'s `tool` directive.

```
tools/go.mod
  tool (
    github.com/bufbuild/buf/cmd/buf
    google.golang.org/protobuf/cmd/protoc-gen-go
    connectrpc.com/connect/cmd/protoc-gen-connect-go
    github.com/golangci/golangci-lint/v2/cmd/golangci-lint
  )
```

`scripts/gen-proto.sh` builds the plugins into `tools/.bin` via
`go install` (with `GOWORK=off` so the toolchain module is read directly),
prepends that directory to `PATH`, and invokes `buf generate`.

### Why local plugins, not remote (BSR) plugins?

Buf supports remote plugins (`buf.build/protocolbuffers/go:vX.Y.Z`) that run
on the Buf Schema Registry. We tried this first and it failed with
`403 Forbidden` from the BSR — the registry is unreachable from some networks
(e.g. the development environment this repo was bootstrapped in). Local
plugins remove that external dependency entirely and make codegen fully
offline-capable, at the cost of building the plugins on first run (~5s).

## Consequences

- **Pros:** zero global installs; versions pinned in `tools/go.mod`; works
  offline; CI verifies generated code is current via `make proto-check`.
- **Cons:** first-run plugin build (~5s); `tools/.bin` must be on `PATH` for
  `buf` to find `protoc-gen-*` (handled by `scripts/gen-proto.sh`).
- **Managed mode:** `buf.gen.yaml` uses buf's managed `go_package_prefix` so
  individual `.proto` files do *not* need a `go_package` option — the import
  path is derived from the file path automatically.

## Alternatives considered

- **Remote BSR plugins:** failed in practice (403); also adds an external
  network dependency to every codegen run. Rejected.
- **Global `protoc` + plugins:** breaks the "no global installs" rule and
  causes version drift across developers. Rejected.
- **Pure `protoc` without buf:** loses linting, breaking-change detection,
  and managed mode. Rejected.
