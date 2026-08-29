# ADR-0013: golang-migrate as the repo-wide migration standard

- **Status:** Accepted
- **Date:** 2026-07-27
- **Phase:** 4 (Auth Service)

## Context

Every service that owns a schema needs a database migration tool, and the
choice should be made once for the whole repo. We want migrations that are
version-controlled, reviewable in PRs, and runnable both at startup (for
self-contained binaries) and from the shell (for operational recovery).

## Decision

**golang-migrate is the repo-wide migration tool.** Each service that owns a
schema embeds its `migrations/` directory via `//go:embed` and runs migrations
at startup via the library. The `migrate` CLI is available through
`tools/go.mod` for manual operations.

SQL files use the standard **`NNNNNN_name.up.sql` / `NNNNNN_name.down.sql`**
convention.

### Standard layout (per service)

Every schema-owning service reproduces the same shape:

- `migrations/` — plain SQL files (`NNNNNN_*.up.sql` / `.down.sql`)
- `migrations/embed.go` — `//go:embed` of the SQL files into the binary
- `internal/infra/migrate` — library runner invoked at startup

## Decision rationale

- **Plain SQL files** — version-controlled, diffable, reviewable in PRs. No
  DSL to learn, no generated code to inspect.
- **Up/down support** — `up` for apply, `down` for rollback in dev and
  incidents.
- **Widely understood** — the tool is well known and well supported; new
  contributors ramp fast.
- **Consistent pattern** — one layout, reproduced per service, so any engineer
  looking at a new service already knows where migrations live.

## Consequences

- **Pros:** one new dependency (`golang-migrate`); migrations are embedded, so
  binaries are self-contained and apply their own schema on boot.
- **Pros:** the `migrate` CLI in `tools/` enables operational recovery (manual
  `up`/`down`/`force`) without rebuilding or redeploying.
- **Cons:** one extra dependency per schema-owning service. Accepted for the
  consistency and self-containment it buys.
- **Trade-off accepted:** we carry `golang-migrate` as a dependency rather than
  reinventing an embed-FS runner.

## Alternatives considered

- **goose:** similar ergonomics, more Go-native (supports Go-based migrations).
  Rejected — we have no need for code-driven migrations, and plain SQL keeps
  the surface uniform and DBA-reviewable.
- **Custom embed-FS runner:** zero dependencies, but reinvents version
  tracking, up/down semantics, and error handling. Rejected as needless
  engineering.
