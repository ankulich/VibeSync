# VibeSync

A production-grade platform for synchronized music listening and synchronized
video watching. Synchronized playback is hard: variable network latency,
client clock skew, host migration, and drift correction all have to be
solved together. This repo is built around that core problem.

**Status:** Phase 1 (Architecture & Foundation) complete. No business
services yet — those land in their own phases per the Generation Order.

---

## What's here

| Path | What it is |
|------|------------|
| `proto/vibesync/` | Canonical `.proto` contracts. Auth and Sync are fully specified; the rest have stable signatures. |
| `gen/go/` | Generated Go code (Connect-RPC + protobuf). Produced by `make proto`. |
| `libs/` | Shared Go libraries: errors, id, config, log, observability, telemetry, plus port interfaces for kafka/outbox/rbac/featureflag/web/rpc/store/redis/mongo/storage/testing. |
| `tools/` | Private Go toolchain module (buf, protoc-gen-*, golangci-lint) pinned via `go tool`. |
| `docker/base.Dockerfile` | Multi-stage distroless base for all services. |
| `deployments/docker-compose/` | Local infra stack: Postgres, Redis, Mongo, Kafka, MinIO, OTel, Prometheus, Grafana, Coturn. |
| `docs/adr/` | Architecture Decision Records (0001–0009). |
| `docs/sync/algorithm.md` | Specification of the synchronization algorithm — the project's technical core. |
| `scripts/` | `bootstrap.sh`, `gen-proto.sh`, `test.sh`. |

## Prerequisites

- **Go 1.26+**
- **Docker Desktop** (for the local infra stack)
- **Git Bash** on Windows (the scripts target Bash; PowerShell works for the
  `go` commands directly)

No global installs of `buf`, `protoc`, or `golangci-lint` are required —
they're pinned in `tools/go.mod` and invoked via `go tool`.

## Quick start

```bash
# 1. Bootstrap the toolchain (builds buf + plugins into tools/.bin).
scripts/bootstrap.sh

# 2. Generate Go code from the .proto contracts.
make proto      # or: scripts/gen-proto.sh

# 3. Boot the local infrastructure.
make docker-up  # or: docker compose -f deployments/docker-compose/docker-compose.yml up -d

# 4. Build and test.
make build
make test
```

## Working with the code

- **Adding a `.proto` change:** edit under `proto/vibesync/`, run `make proto`,
  commit both the `.proto` and the regenerated `gen/go/`. CI runs
  `make proto-check` to verify generated code is current.
- **Adding a library:** create a package under `libs/`. It shares the
  `vibesync/libs` module — no new `go.mod` needed.
- **Adding a service:** create `apps/<svc>/` with its own `go.mod`, then
  `go work use ./apps/<svc>` to add it to the workspace.

## Local stack endpoints

Once `make docker-up` has run:

| Service       | URL / port             | Credentials        |
|---------------|------------------------|--------------------|
| Postgres      | localhost:5432         | vibesync / vibesync |
| Redis         | localhost:6379         | (none)             |
| MongoDB       | localhost:27017        | vibesync / vibesync |
| Kafka (external) | localhost:9094      | (none, PLAINTEXT)  |
| MinIO API     | localhost:9000         | vibesync / vibesync123 |
| MinIO console | localhost:9001         | vibesync / vibesync123 |
| Jaeger UI     | http://localhost:16686 | (none)             |
| Prometheus    | http://localhost:9090  | (none)             |
| Grafana       | http://localhost:3000  | admin / admin      |
| Coturn (TURN) | localhost:3478         | vibe / vibe        |

## Troubleshooting

### `go test` fails with "Access is denied" on Windows

This is a Windows Defender real-time-scan artifact: the test binary
(`*.test.exe`) is locked while being written to the build cache. `make test`
sets `GOTMPDIR` to a workspace-local directory which is typically trusted.
If it persists:

- Add the repo directory to Windows Defender's exclusion list, or
- Run a specific package's tests via `go test -c -o ./bin/x.test.exe ./libs/<pkg> && ./bin/x.test.exe`.

### `buf generate` fails with 403 Forbidden

You're hitting the Buf Schema Registry (BSR), which is unreachable from some
networks. Our config uses **local plugins** (not BSR), so this should not
happen — but if it does, ensure `scripts/gen-proto.sh` is the entry point
(rather than calling `buf generate` directly), so the local plugins are
built and on `PATH`.

### Docker daemon not running

`make docker-up` requires Docker Desktop to be running. Start it manually on
macOS/Windows, or `systemctl start docker` on Linux.

## Architecture decisions

See `docs/adr/` for the full trail. Headline decisions:

1. **Workspace layout** — `go.work`, one module per service, shared `libs`.
2. **Proto toolchain** — buf + local plugins via `go tool`; no global installs.
3. **RPC transport** — Connect-RPC (gRPC + HTTP/JSON from one schema).
4. **Event-driven** — outbox + Kafka + idempotent consumers + versioned topics.
5. **Error model** — typed `*errors.Error` with stable reason strings.
6. **Sync clock** — authoritative `(media_time, wall_time)` pair + epoch + fencing.
7. **Config** — Viper, env-first, secrets via env only.
8. **Observability** — slog + OpenTelemetry + Prometheus, one import per service.
9. **Identifiers** — ULID everywhere; time-sortable, collision-free.

The sync algorithm is specified in `docs/sync/algorithm.md`.
