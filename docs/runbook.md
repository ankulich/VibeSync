# VibeSync Operations Runbook

Day-to-day operations for the VibeSync local development stack: how to boot
everything, how to keep it healthy, and what to do when it isn't.

All commands assume the repo root (`E:\repos\VibeSync` or your clone) and a
Bash shell (Git Bash on Windows). Infrastructure runs in Docker; the seven Go
services and the frontend run on the host.

---

## Quick Start

Prerequisites: Go 1.26+, Docker Desktop (running), Node.js for the frontend,
Git Bash on Windows.

```bash
# 1. Bootstrap the pinned toolchain (buf, protoc plugins, linters).
scripts/bootstrap.sh

# 2. Generate Go code from the proto contracts (skip if gen/go is current).
make proto          # or: scripts/gen-proto.sh

# 3. Boot the infrastructure stack (Postgres, Redis, Mongo, Kafka, MinIO,
#    OTel collector, Jaeger, Prometheus, Grafana, Coturn).
make docker-up      # or: docker compose -f deployments/docker-compose/docker-compose.yml up -d

# 4. Wait until the stack is healthy (optional but recommended):
docker compose -f deployments/docker-compose/docker-compose.yml ps

# 5. Start the backend services (one terminal each, or in the background):
go run ./apps/auth-service/cmd/auth-service
go run ./apps/user-service/cmd/user-service
go run ./apps/room-service/cmd/room-service
go run ./apps/sync-service/cmd/sync-service
go run ./apps/playback-service/cmd/playback-service
go run ./apps/media-service/cmd/media-service
go run ./apps/provider-service/cmd/provider-service

# 6. Start the frontend (Vite dev server on http://localhost:5173):
cd apps/frontend && npm install && npm run dev
```

The Vite dev server proxies Connect-RPC paths to the matching backend by
package prefix (`/vibesync.auth` -> :8080, `/vibesync.user` -> :8081, ...),
so the frontend and backends must both be running for end-to-end flows.

Verify the stack: open Grafana (http://localhost:3000) and check the
"VibeSync — Platform Overview" dashboard — "Services Up" should read 7/7.

---

## Service Lifecycle

The seven services, their ports, and their default database:

| Service           | Port | Command                                      | Logical DB |
|-------------------|------|----------------------------------------------|------------|
| auth-service      | 8080 | `go run ./apps/auth-service/cmd/auth-service` | `auth`     |
| user-service      | 8081 | `go run ./apps/user-service/cmd/user-service` | `user`     |
| room-service      | 8082 | `go run ./apps/room-service/cmd/room-service` | `room`     |
| sync-service      | 8083 | `go run ./apps/sync-service/cmd/sync-service` | `sync`     |
| playback-service  | 8084 | `go run ./apps/playback-service/cmd/playback-service` | `playback` |
| media-service     | 8085 | `go run ./apps/media-service/cmd/media-service` | `media`    |
| provider-service  | 8086 | `go run ./apps/provider-service/cmd/provider-service` | `provider` |

Each service runs in the foreground in its own terminal.

- **Stop**: press `Ctrl+C` in the service's terminal (graceful shutdown).
- **Restart**: stop it, then run the same `go run` command again.
- **Rebuild after code changes**: just restart — `go run` recompiles.
- **All at once**: there is no supervisor on the host; run each in a
  separate terminal, or background them with `&` and `kill %N` to stop.

### Health probes

Every service mounts the same health endpoints on its main listener
(`libs/web/web.go`):

- `GET /healthz` — liveness. Returns `200 {"status":"ok"}` whenever the
  process is up. Use this for "is it running".
- `GET /readyz` — readiness. Returns `503 NOT_READY` until every subsystem
  (DB pool, Kafka, Redis) reports healthy, then `200 {"status":"ready"}`.
  Use this for "can it take traffic".

```bash
curl http://localhost:8080/healthz   # auth-service alive?
curl http://localhost:8082/readyz    # room-service ready?
```

A service that stays on `readyz: 503` usually means a backing dependency is
down (see Common Issues below) — check its terminal output.

---

## Common Alerts & Responses

Alert rules live in `deployments/docker-compose/alerts.yml` and are visible
at http://localhost:9090/alerts (there is no Alertmanager in the dev stack).
The four that matter:

### `ServiceDown` (critical)

- **Meaning**: `up{job="vibesync-services"} == 0` for 1 minute — the
  service's `/metrics` endpoint is unreachable. The process has crashed,
  exited, or is hung.
- **Check**: which instance label fired (the port maps to the service, e.g.
  `host.docker.internal:8082` = room-service). `curl localhost:8082/healthz`.
  Look at the service terminal for a panic or fatal log. Check the
  "Services Up" stat on the Platform Overview dashboard.
- **Fix**: restart the service (`go run ./apps/<svc>/cmd/<svc>`). If it
  crashes on startup, it is almost always a missing dependency (Postgres,
  Redis, Kafka not up) or a missing env var (e.g. `VB_AUTH_KEY_MASTER` for
  auth-service) — the startup log names the failing subsystem. If the whole
  host was rebooted, re-run `make docker-up` first, then start services.

### `HighErrorRate` (warning)

- **Meaning**: more than 5% of RPC responses carry a non-OK gRPC status
  code for 5 minutes on one service.
- **Check**: open "VibeSync — Service Detail", select the service, look at
  "Error Rate %" and "Request Rate by Method" to find the failing method.
  Then Jaeger (below): filter traces by that service and look for spans
  ending in error status. The status reason string comes from the typed
  error model (ADR-0005) and names the failing condition.
- **Fix**: depends on the reason — `UNAVAILABLE` on downstream calls means a
  dependency is down (check Docker containers); `FAILED_PRECONDITION`/
  `NOT_FOUND` storms usually mean a bad deploy or missing migration
  (run the service's migrations). `INVALID_ARGUMENT` spikes are client bugs.

### `HighLatency` (warning)

- **Meaning**: p99 RPC latency above 1 second for 5 minutes on one service.
- **Check**: "Service Detail" -> "Latency by Method p95" to isolate the
  slow method; "Go Runtime" dashboard for GC pauses, goroutine pileups, or
  memory pressure; Jaeger for where in the span tree the time goes (DB
  query vs. downstream call vs. lock wait).
- **Fix**: slow Postgres queries -> check the DB (`pg_stat_activity` for
  lock waits); connection-pool exhaustion -> the service log names pool
  errors, restart clears it; GC thrashing -> visible on the Go Runtime
  dashboard, usually a response payload that grew too large.

### `CollectorDown` (critical)

- **Meaning**: `up{job="otel-collector"} == 0` for 1 minute — the OTel
  collector's Prometheus exporter is unreachable. Traces and all
  `rpc_server_duration_*` metrics stop flowing; direct-scrape Go/process
  metrics keep working.
- **Check**: `docker ps | grep vibesync-otel`, then
  `docker logs vibesync-otel`. Grafana's RPC panels go empty while
  goroutine/CPU panels keep updating — that pattern is the tell.
- **Fix**: `docker compose -f deployments/docker-compose/docker-compose.yml
  restart otel-collector`. Services buffer and retry OTLP exports, so data
  recovers on its own once the collector is back.

---

## Log Locations

Services log **structured JSON to stdout** (slog, via `libs/log`). There are
no log files — logs live in the terminal the service was started in. To
capture them:

```bash
go run ./apps/sync-service/cmd/sync-service 2>&1 | tee /tmp/sync.log
```

Infrastructure containers log to the Docker logger:

```bash
docker compose -f deployments/docker-compose/docker-compose.yml logs -f otel-collector
docker logs vibesync-kafka --tail 100 -f
```

Every log line carries `trace_id` and `span_id` injected from the current
OpenTelemetry span context (`libs/log/log.go`), so any log line can be
jumped to a full trace in Jaeger:

```json
{"time":"2026-08-19T10:14:32.114Z","level":"INFO","msg":"rpc handled",
 "service":"sync-service","rpc_method":"GetClock","status":"ok",
 "duration_ms":3,"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736",
 "span_id":"00f067aa0ba902b7"}
```

---

## Trace Debugging

Jaeger UI: **http://localhost:16686**. Services push OTLP to the collector
(`localhost:4317` gRPC / `4318` HTTP), which exports to Jaeger.

**Find a trace by trace ID** (the usual flow):

1. Grab the `trace_id` out of a JSON log line for the failing request.
2. In Jaeger UI, left menu -> the search form; if a trace-ID field is not
   visible, expand the search options and paste the ID into "Find a trace
   by its ID", press Find. The full span tree across all services opens.

**Browse by service**:

1. Service dropdown -> pick e.g. `room-service`.
2. Optionally set Operation (the RPC method), Tags (`error=true`), and a
   time range (default is last hour — widen it if traffic is sparse).
3. Find Traces -> pick a trace; waterfall shows every service the request
   touched, with per-span durations and attributes.

Correlating: pick the same time window in Grafana, note the timestamp of
the latency/error spike, then find the matching trace in Jaeger.

---

## Dashboards

Grafana: **http://localhost:3000**, login `admin` / `admin`. Dashboards are
file-provisioned from `deployments/docker-compose/grafana/dashboards/` into
the **VibeSync** folder (edits in the UI are overwritten on container
restart — edit the JSON to persist changes).

| Dashboard | UID | What it answers |
|-----------|-----|-----------------|
| VibeSync — Platform Overview | `vibesync-overview` | Is everything up? Fleet-wide traffic, error %, p50/p95/p99, goroutines. |
| VibeSync — Service Detail | `vibesync-service-detail` | Drill into one service (the `Service` variable): rate, errors, per-method latency and payload sizes, CPU, memory. |
| VibeSync — Go Runtime | `vibesync-go-runtime` | Scheduler/memory internals for every service: goroutines, threads, heap alloc vs sys, GC pauses, open FDs. |

RPC panels read from the `otel-collector` job (OTLP-pushed
`rpc_server_duration_milliseconds_*`); goroutine/CPU/memory panels read
from the direct `vibesync-services` scrape. In Service Detail, the
CPU/memory panels show all instances — map by port (8080 auth ... 8086
provider, per the table above).

---

## Database Connections

Postgres: **localhost:5432**, user/password `vibesync` / `vibesync`.
Eight logical databases, one per owning service (plus storage for
media/object metadata), created on first boot:

`auth`, `user`, `room`, `sync`, `playback`, `media`, `storage`, `provider`

```bash
# Connect to a service's DB:
psql -h localhost -U vibesync -d room
# Password: vibesync

# Inside psql — the usual checks:
\dt                      -- tables (incl. outbox)
select * from outbox order by created_at desc limit 5;
select pid, state, wait_event_type, query from pg_stat_activity;

# List databases / sizes:
psql -h localhost -U vibesync -d vibesync -c "\l"
```

Data persists in the `pg_data` volume; `make docker-down` (with `-v`) wipes
it. Migrations use golang-migrate (ADR-0013) and run as part of service
startup. Stuck locks after a crash: `select pg_cancel_backend(<pid>);` from
`pg_stat_activity`.

---

## Kafka

Broker: **localhost:9094** from the host (PLAINTEXT, no auth);
`kafka:9092` inside the Docker network. Single broker, KRaft mode,
auto-create on, 3 partitions per topic. Topics are `<aggregate>.<event>.v1`:

| Topic | Producer | Consumer |
|-------|----------|----------|
| `user.created.v1` | auth-service (OAuth signup, via outbox) | user-service |
| `room.created.v1` | room-service (outbox) | sync-service |
| `room.joined.v1` | room-service (outbox) | — |
| `room.deleted.v1` | room-service (outbox) | — |
| `sync.updated.v1` | sync-service (outbox) | playback-service |

Consumer groups are named after the consuming service (`user-service`,
`sync-service`, `playback-service`). Every consumer is idempotent
(ADR-0015) and every producer goes through the transactional outbox, so
re-processing and at-least-once delivery are safe.

```bash
# All Kafka CLI tools exec into the container:
K="docker exec vibesync-kafka /opt/bitnami/kafka/bin"

# List topics / describe one:
$K kafka-topics.sh --bootstrap-server localhost:9092 --list
$K kafka-topics.sh --bootstrap-server localhost:9092 --describe --topic room.created.v1

# Watch events live (host listener :9094):
$K kafka-console-consumer.sh --bootstrap-server localhost:9094 \
  --topic user.created.v1 --from-beginning

# Consumer group lag (the key health metric):
$K kafka-consumer-groups.sh --bootstrap-server localhost:9092 --list
$K kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
  --describe --group sync-service
```

Growing lag: the consumer service is down or stuck — check its terminal,
restart it, then verify the group's lag drains.

---

## Redis

Redis: **localhost:6379**, no password, DB 0. 256 MB maxmemory, `allkeys-lru`
— it holds only cache/coordination state, never a source of truth.

What each service uses it for:

| Service | Use |
|---------|-----|
| auth-service | Login rate limiting; JWKS cache |
| user-service | Kafka consumer idempotency (dedup window) |
| room-service | Outbox relay leader election |
| sync-service | Room presence — sorted sets keyed `sync:presence:<roomID>` |
| playback-service | Kafka consumer idempotency |
| media-service | Outbox relay leader election |
| provider-service | Spotify client-credentials token cache |

```bash
docker exec -it vibesync-redis redis-cli

# Inside redis-cli:
keys sync:presence:*                    # who's in which rooms
zrange sync:presence:<roomID> 0 -1 withscores   # members + last-seen
ttl spotify:token:*                     # cache TTLs
memory usage sync:presence:<roomID>
```

If Redis restarts: leader-election locks and presence re-form within
seconds; idempotency windows reset (safe — consumers are idempotent via
upsert); token caches simply refill.

---

## Common Issues

### Windows Defender locks the Go test binary ("Access is denied")

On Windows, Defender's real-time scan holds the freshly-built `*.test.exe`
in the default temp directory while writing, so `go test` fails with
"Access is denied". `scripts/test.sh` (used by `make test`) works around it
by pointing `GOTMPDIR` at a workspace-local `.tmp` directory, which is
typically trusted. If it still happens:

- Add the repo directory to Windows Defender's exclusion list, or
- Build and run the test binary explicitly:
  `go test -c -o ./bin/x.test.exe ./libs/<pkg> && ./bin/x.test.exe`.

### Empty/nonexistent temp dir handle

The same Defender behavior can leave the default temp dir handle held open,
and setting `GOTMPDIR` to a path that doesn't exist makes `go` fail with
"The system cannot find the file specified". Always create the dir before
use:

```bash
mkdir -p .tmp
export GOTMPDIR="$(pwd)/.tmp"
```

(`make test` does this for you; only needed when invoking `go test`
directly.)

### Service startup order

Services fail fast when required dependencies are missing: a service
started before Postgres/Redis/Kafka are healthy will log a connection error
and either exit or sit not-ready (`/readyz` returns 503). Order:

1. `make docker-up` — wait for `docker compose ... ps` to show all
   containers healthy (Kafka takes ~30s on cold start).
2. Start the Go services.
3. Start the frontend.

If the frontend is started first it still loads, but API calls fail until
the backends are up (Vite proxies return 502/ECONNREFUSED). Services that
crashed at boot just need to be started again once the stack is healthy —
no data is lost. The OTel collector being down does not block startup:
telemetry is retried in the background.

### Other quick checks

- Docker daemon not running: start Docker Desktop first (`make docker-up`
  will fail otherwise).
- Grafana dashboards empty: check that Prometheus is scraping —
  http://localhost:9090/targets should show `vibesync-services` (7 targets)
  and `otel-collector` as UP.
- Port already in use: a previous service instance is still running — find
  it (`netstat -ano | grep 8080` then taskkill by PID on Windows) or just
  close its terminal.
