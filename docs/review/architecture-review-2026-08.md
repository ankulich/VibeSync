# Architecture Review — 2026-08-30

Scope: full-repo review covering service architecture, the event-driven
pipeline (Kafka/outbox), inter-service wiring, the Docker/Compose setup, and
the frontend's link/feature surface. Findings are ranked by severity; each
includes the exact location and one or more proposed remediation options.

**Fixed in this pass** (see "Fixes shipped 2026-08-30" below): the
registration 502 chain, the outbox payload corruption, missing Postgres
databases, compose wiring gaps, Docker build consolidation, per-service
redeploy workflow, and the missing login→register link. Everything else in
this document is a **proposal for follow-up work**, not yet implemented.

---

## 1. Executive summary

The repo is a disciplined hexagonal-style monorepo: 7 Go services over one
Postgres/Redis/Kafka stack, Connect-RPC for sync calls, transactional
outbox + Kafka for async, per-service logical databases, embedded
golang-migrate migrations, and a React frontend proxied by nginx. The bones
are good and unusually consistent for a codebase of this size.

Three systemic problems stand out:

1. **There is no authentication boundary.** Every service trusts client
  -supplied `X-Vibesync-User-Id` / `X-Vibesync-System-Role` headers, and
   nothing validates the `Authorization: Bearer` JWT that the frontend
   dutifully sends. Identity — and admin role — is spoofable on every
   service.
2. **The event pipeline was silently broken end-to-end.** A double-encoding
   bug in every outbox writer base64-wrapped all payloads, so all consumers
   skipped 100% of domain events as "malformed" while committing offsets.
   Combined with two boot-time crashes, registration returned 502. (Fixed.)
3. **~1,500 lines of event machinery are copy-pasted across four services**
   with drift already visible, while five `libs/*` port packages ship with
   zero consumers. The shared-code strategy the ADRs describe (relay "ships
   with Auth, reused by every producing service") was never executed.

---

## 2. Fixes shipped 2026-08-30

| # | Fix | Files |
|---|-----|-------|
| 1 | `bootstrapSigner` called an undefined `isNotFoundErr` (build break; misclassified typed NotFound as fatal → auth crash-looped → nginx 502). Now checks `errors.Is(err, ports.ErrNotFound)`. | `apps/auth-service/cmd/auth-service/main.go` |
| 2 | Outbox writers re-marshaled the already-serialized `Payload []byte`, base64-wrapping every event so consumers saw a JSON string instead of an object and dropped it. Writers now insert the payload verbatim. | `apps/{auth,room,sync,media}-service/internal/infra/postgres/outbox_writer.go` |
| 3 | New one-shot `postgres-init` compose job runs the idempotent multi-DB script on every `up` — heals `pg_data` volumes created before the init-script mount (root cause of `FATAL: database "user" does not exist` crash loops in 6 services). | `deployments/docker-compose/docker-compose.yml` |
| 4 | `user-service` was given `VB_KAFKA_CONSUMER_TOPIC`, a key it doesn't read (its config key is `kafka.topic` → `VB_KAFKA_TOPIC`); it worked only by default-value coincidence. Compose now sets the real key. | same |
| 5 | `depends_on` gaps: media-service needs Redis+Kafka (relay/producer), auth needs Kafka (outbox relay), frontend now waits for all 7 backends. | same |
| 6 | 7 near-identical per-service Dockerfiles + drifted, unused `docker/base.Dockerfile` (wrong `ENTRYPOINT /app/service`, fictional `VB_HTTP_ADDR`/`VB_RPC_ADDR` contract) replaced by one parameterized `docker/service.Dockerfile`; every service tagged `vibesync/<svc>:local`. | `docker/service.Dockerfile`, compose |
| 7 | Root `.dockerignore` added — previously every Go service build shipped `apps/frontend/node_modules` and `.git` to the daemon. Frontend Dockerfile gains an npm cache mount. | `.dockerignore`, `apps/frontend/Dockerfile` |
| 8 | Makefile: `svc-up/build/restart/logs S=<svc>` (redeploy one service with `--no-deps`, no cascading restarts), `docker-up` now `up -d --wait`, `docker-down` keeps volumes, `docker-reset` wipes them; `build`/`lint` now include `apps/*` (previously no application code was compiled or linted by make). | `Makefile` |
| 9 | Login page now links to the registration page. | `apps/frontend/src/pages/LoginPage.tsx` |
| 10 | Init script emitted `CREATE DATABASE user` — `user` is a reserved word; identifiers are now quoted (the first-boot path had never actually run, so this was latent). | `deployments/docker-compose/init-scripts/postgres-multi-db.sh` |
| 11 | media-service migration used `gin_trgm_ops` without creating `pg_trgm` — the service could never migrate a fresh DB. Added `CREATE EXTENSION IF NOT EXISTS pg_trgm` (+ drop in the down migration). | `apps/media-service/migrations/000001_init.{up,down}.sql` |
| 12 | New one-shot `kafka-init` job pre-creates all five topics. Without it, consumer groups joined before auto-create fired and were assigned **zero partitions forever** (verified: group "Stable", 0 partitions, no consumption) — `user.created.v1` was published but never consumed until the consumer restarted. Consumers now depend on `kafka-init: service_completed_successfully`. | compose |
| 13 | Four healthchecks that never worked: mongo (YAML `>`-folded `test` became one string, shell tried to exec `["CMD-SHELL",`), coturn (NUL bytes in argv → `exec /bin/sh: invalid argument`), otel (distroless-style image, no shell/wget — healthcheck removed with a comment), frontend (`localhost` → ::1 while nginx binds IPv4 only → now 127.0.0.1). | compose |
| 14 | nginx cached upstream DNS at config load, so recreating any backend (new container IP) made its `proxy_pass` return 502 until the frontend restarted — which silently defeated per-service updates. Upstreams now go through variables + `resolver 127.0.0.11 valid=10s` (per-request resolution). | `apps/frontend/nginx.conf` |
| 15 | auth-service crashed on every restart after the first boot: with `VB_AUTH_KEY_MASTER` unset it generated an **ephemeral** master key, encrypted the signing key with it, and the next boot's fresh ephemeral key couldn't decrypt it (`decrypt active key: message authentication failed` → crash loop → 502). Compose now defaults to a stable dev master key (base64, 32 bytes; override via env outside dev), and the decrypt-failure error names the likely cause. Existing dev databases created under an ephemeral key need a one-time `TRUNCATE auth.signing_keys` to re-bootstrap. | compose, `apps/auth-service/cmd/auth-service/main.go` |
| 16 | Every room/room-adjacent RPC failed with `PERMISSION_DENIED: not allowed to room.create`: the web frontend sent `X-Vibesync-System-Role: USER` (protobuf-es reverse-maps enums to **short** names), while every Go service parsed only the **full** proto name (`SYSTEM_ROLE_USER`) and silently downgraded unknown spellings to UNSPECIFIED. The six copy-pasted `parseSystemRole` helpers were replaced by one tolerant `vbweb.ParseSystemRole` in `libs/web` (full name / short name / numeric wire value; anything else fails closed) with unit tests — also the first installment of the H2 de-duplication. | `libs/web/subject.go`, all 7 `internal/app` packages |

Registration flow after the fixes: `POST /vibesync.auth.v1.AuthService/Register`
→ auth tx (user row + `user.created.v1` outbox row) → relay → Kafka →
user-service consumer → read model. Verified end-to-end via
`docker compose up -d --wait` + register request returning tokens and the
consumer logging `projected user`.

---

## 3. Event graph (topics ↔ services)

| Topic | Producer | Consumer | Status after fixes |
|---|---|---|---|
| `user.created.v1` | auth (register, oauth) | user-service (read model) | **working** |
| `room.created.v1` | room-service | sync-service (room init) | **working** |
| `room.joined.v1` | room-service | — | **dangling**: no consumer; membership events vanish |
| `room.deleted.v1` | room-service | — | **dangling**: sync room state + playback cache are never torn down |
| `sync.updated.v1` | sync-service | playback-service (room cache) | **working** |
| `media.*.v1` | media-service *can* produce (producer/relay wired) | — | **dormant**: no use case appends an outbox event; the relay polls a table nobody writes |

Proposals:

- **room.deleted.v1 consumers** (small, high value): sync-service handler
  calling a new `RoomManager.Remove(roomID)` (stop the room goroutine,
  delete state row, drop Redis presence key), and playback-service evicting
  its room cache entry. Mirrors the existing `room.created.v1` handler
  (`apps/sync-service/internal/app/handlers.go:201`).
- **room.joined.v1**: either consume it (e.g. sync presence seed, future
  analytics) or stop emitting it until a consumer exists — emitting events
  nobody reads costs an outbox row + Kafka write per join.
- **media-service**: stage `media.queued.v1` / `media.removed.v1` from the
  queue use cases (the infra is already wired in
  `apps/media-service/internal/infra/events/events.go`), giving playback a
  real event source instead of polling.

Kafka topics are now created explicitly by the one-shot `kafka-init` job on
every `up` (fix #12) — do not remove it when hardening the broker for
production; auto-create can then be safely disabled.

---

## 4. Findings (not yet fixed), ranked

### Critical

**C1. Identity is spoofable on every service.**
Every handler derives the caller from client-supplied headers:
`X-Vibesync-User-Id` / `X-Vibesync-System-Role` (`apps/auth-service/internal/app/providers.go:74`,
`apps/user-service/internal/app/handlers.go:168`,
`apps/room-service/internal/app/service.go:93`,
`apps/sync-service/internal/app/service.go:88`,
`apps/media-service/internal/app/service.go:98`,
`apps/provider-service/internal/app/service.go:98`). Comments say "set by
the API Gateway" — no gateway exists; nginx passes headers through
(`apps/frontend/nginx.conf`), `libs/web` allow-lists them in CORS
(`libs/web/web.go:224`), and the frontend itself sends them
(`apps/frontend/src/api/transport.ts:44`). The `Authorization: Bearer` JWT
the frontend also sends is never verified anywhere. Anyone can be any user
or an administrator.

Options:
- (a) Add a JWT-verification interceptor in `libs/rpc` (validate signature
  via auth's `GetJwks` — the primitives already exist and are unused —
  cache JWKS, check `exp`/`aud`), map claims → subject, strip incoming
  identity headers at the edge, then delete all `subjectFromHeader`
  copies. ~1–2 days, touches all services + frontend transport.
- (b) If an API gateway is really planned (ADR overview says so), land it
  first and have it verify JWTs and inject trusted headers on an isolated
  network — but until then (a) is required; the current state is not
  "gateway pending", it's "open".

**C2. `GetProviderToken` returns decrypted third-party OAuth tokens with no
authorization.** `apps/auth-service/internal/app/provider_token.go:26` —
commented "Internal-use (Provider Service)" but no interceptor, network
boundary, or service credential exists; provider-service calls it with no
headers (`apps/provider-service/internal/infra/authclient/client.go`).
Unauthenticated callers can drain any user's Spotify/Google tokens.
Options: internal-service mTLS or shared-secret interceptor + nginx rule
refusing to proxy this method from outside; simplest first step is an
interceptor requiring a service token for `auth.v1.AuthService/GetProviderToken`
and `Introspect`.

### High

**H1. Consumer failure semantics are unsound; no DLQ (ADR-0004 promised
both).** On handler error the consumers "don't commit and continue"
(`apps/user-service/internal/app/consumer.go:81`,
sync `events.go`, playback `consumer.go`), but committing any *later*
message in the same partition implicitly commits the failed offset —
failures are silently skipped, contradicting the at-least-once comments.
Poison messages either vanish (the old double-encode path) or spin a 1s
retry loop forever. Proposal: extract one shared consumer runner into
`libs/kafka` with bounded retries → `<topic>.dlq` + commit, and a metric
per dead-lettered event. Replace the three per-service loops.

**H2. ~1,500 lines of duplicated event machinery.** `Producer`,
`Publisher`, `Relay`, `RedisLocker`, `OutboxWriter/FetchUnpublished/
MarkPublished`, plus `pool.go`, redis client, `systemClock`, `ulidGen`,
`postgresURL`, `loadConfig` are copy-pasted across services; the copies
have drifted (auth's relay uses a `PublisherAdapter` interface, the others
concrete types; room's carries a dead `var _ = json.Marshal`). Proposal:
move to `libs/outbox` (schema-qualified table name per service as a
parameter), delete the copies. The `Tx`/`Rows` indirection in
`libs/outbox` can be simplified to `pgx.Tx` at the same time — the current
"sanctioned" `libs/store.InTx` has zero callers, so pick one pattern.

**H3. Outbox relay leader lock is not atomic.** Release is GET-then-DEL
(`apps/room-service/internal/infra/events/relay.go:196`,
sync `events.go:300`, media `events.go:241`; auth's copy in
`internal/infra/events/adapters.go:72` admits the gap). A stale leader can
DEL a fresh leader's key between GET and DEL. `libs/redislib.Lock`
documents the fencing-token contract and is unused. Fix as part of H2 with
a Lua compare-and-delete.

### Medium

**M1. Rate limiting is a configured no-op.** `libs/web/web.go:251` —
`rateLimitMiddleware` is `c.Next()`; every service config sets
`rate_limit_per_second: 10`; auth contains a complete Redis fixed-window
`RateLimiter` (`apps/auth-service/internal/infra/rate_limit.go`) that is
never constructed. Wire it in auth (login/register/refresh) and either
implement a token bucket in `libs/web` or delete the config key from the
other services.

**M2. `atomicBool` is not atomic.** `libs/web/web.go:95` — plain `uint32`
read/write from concurrent goroutines (`SetReady` vs `/readyz` handler);
a data race by construction. Use `sync/atomic.Bool`. One-line fix, do it
with the next `libs/web` touch.

**M3. Health endpoints check nothing.** `/healthz` is a constant 200 and
`/readyz` a bool (`libs/web/web.go:79`); every service has `Pool.Ping`
that no probe calls. Compose healthchecks therefore cannot see a dead
DB/Redis/Kafka. Proposal: extend `vbweb` readiness with an optional pinger
list; wire `pool.Ping`, redis `Ping`, and (for consumers) a last-poll
timestamp.

**M4. Internal error strings leak to clients.** ADR-0005 forbids it, yet
`vberr.Internal("X_FAILED", err.Error())` is routine
(`apps/user-service/internal/app/handlers.go:47`,
`apps/auth-service/internal/app/providers.go:57`, and others); the
`Internal` constructor is also called with inconsistent shapes (with/without
domain). Sweep: log the cause, return a stable reason + correlation id.

**M5. `GetUser` has no authorization check.**
`apps/user-service/internal/app/handlers.go:25` — any caller can enumerate
users (emails, usernames). Fix alongside C1 (owner-or-admin), or at minimum
remove email from the projection.

**M6. Startup config validation is absent** (ADR-0007 explicitly promised
it): zero-value configs (empty DB name, no brokers) boot and fail late or
degrade silently. Add a `cfg.Validate()` in each `loadConfig` and fail
fast.

### Low

- **L1.** Three conventions for one config concept: consumer topic key is
  `kafka.topic` (user, playback) vs `kafka.consumer_topic` (sync); auth
  carries a dead `kafka.topic` default `"vibesync"` that nothing reads
  (topics travel per-event in outbox rows). Normalize when H2 lands.
- **L2.** `libs/rpc.recoverToInternal` swallows the panic value without
  logging (`libs/rpc/rpc.go:111`) — panics vanish. Log with stack.
- **L3.** Repo hygiene: stray `ecommerce-platform/` dir, built
  `host.exe`/`listener.exe` under `apps/test-clients/`, `apps/*/.tmp`
  dirs. `test-clients/seed` hand-duplicates auth's argon2 parameters
  (`apps/test-clients/cmd/seed/main.go:27`) — will silently break when
  auth's parameters change.
- **L4.** Infra images unpinned (`minio`, `coturn`, `otel-collector`,
  `jaeger`, `prometheus`, `grafana` on `:latest`) — pin digests/tags for
  reproducible dev stacks.
- **L5.** `/metrics` is mounted per-service by each `server/http.go`
  although `libs/web` docs claim to do it; `libs/rpc.Options.
  MetricsRegisterer` is accepted and never read. One of the two should go.
- **L6.** OTLP export is misconfigured: services push with TLS to the
  collector's plaintext `:4317`, so every service logs
  `tls: first record does not look like a TLS handshake` every ~15s and
  drops all traces/metrics. Either enable an insecure option in
  `libs/observability` (env-gated, dev) or serve TLS on the collector.
  Pre-existing; not fixed in this pass.

---

## 5. Frontend link / feature audit

All routes resolve; no dead `href="#"`. Gaps are **missing affordances**
for RPCs that exist and work:

| Declared / implied in UI | Reality |
|---|---|
| Login page → registration | **was missing, fixed** (`LoginPage.tsx`, link to `/register`) |
| Rooms empty state: "…invite your friends" (`RoomsPage.tsx:231`) | No invite feature anywhere; proto has `invite_code` on room, unused. Either build invite links or soften the copy |
| Room page | No **Leave room** button (`Room.LeaveRoom` RPC exists) — users can only close the tab; presence lingers until heartbeat timeout |
| Room page | No edit/delete room UI (`UpdateRoom`, `DeleteRoom` exist; deletion also has no consumer — see event graph) |
| Member list | Read-only; `KickMember`, `UpdateMemberRole` have no UI |
| Profile | No edit form (`User.UpdateUser` exists); `ListUsers` unused |
| Player | `SET_RATE` command supported by proto + backend, no UI control sends it |
| Upload | `MediaSource.UPLOAD` + entire `StorageService` (presign up/download) generated but unused; queueing is search-only |

These are product decisions more than defects; the highest-value additions
are **Leave room** (correctness: presence cleanup) and **Delete room**
(closes the `room.deleted.v1` loop once its consumer exists).

---

## 6. ADR vs reality

| ADR | Promise | Reality |
|---|---|---|
| 0003 Connect | Auth interceptor on RPC layer | None exists (C1) |
| 0004 Events | Relay shared from Auth; DLQ; Mongo immutable event log + replay | Relay forked ×4 (H2); no DLQ (H1); no Mongo usage — `libs/mongo` has zero importers while a Mongo container runs |
| 0004/overview | API gateway does authn/routing/rate-limit | No gateway; nginx is pass-through (C1, M1) |
| 0005 Errors | No internal leakage; app layer never imports Connect | `err.Error()` embedded in client-visible errors; handlers take `*connect.Request` throughout (M4) |
| 0007 Config | Startup validation in each main | Absent (M6) |
| 0001 | 11 services eventually | 7 exist; notification/analytics/storage/gateway only as protos; compose creates an unused `storage` DB |

## 7. Unused libs inventory (candidates: adopt or delete)

Zero importers today: `libs/store`, `libs/rbac`, `libs/redislib`,
`libs/mongo`, `libs/storage`, `libs/featureflag`. Partially dead:
`libs/rpc.Server/NewServer` (every main builds its own `http.Server`),
`libs/outbox.Relay/Tx/Rows`, `libs/web.rateLimitMiddleware` +
`MetricsRegisterer`, `libs/testing` (adopted by no app test).

Recommendation: don't delete preemptively — `rbac`/`redislib` become the
natural home when fixing C1/H3 — but every port package with no adoption
path after those fixes should be removed rather than maintained as
aspirational interface soup.

## 8. Suggested order of follow-ups

1. **C1 + C2 + M5** — authentication boundary (JWT interceptor, protect
   token endpoints, authorize GetUser). Nothing else is meaningful until
   identity is real.
2. **H1** — shared consumer with bounded retries + DLQ; add an outbox→
   relay→consumer integration test (the bug fixed today was invisible to
   unit tests because they constructed Kafka values directly).
3. **H2 + H3 + L1** — extract the event machinery into `libs/outbox`,
   safe Redis lock, normalize config keys.
4. **Event graph completion** — `room.deleted.v1` consumers, media events,
   optional `kafka-init`.
5. **M1–M6** — rate limit, atomic bool, deep healthchecks, error hygiene,
   config validation.
6. **Frontend affordances** — Leave room, Delete room, invite links.
