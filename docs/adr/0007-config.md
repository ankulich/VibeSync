# ADR-0007: Configuration management

- **Status:** Accepted
- **Date:** 2026-07-26
- **Phase:** 1 (Architecture)

## Context

Each service needs configuration for: database connection strings, broker
addresses, listening ports, log levels, sample rates, and per-deployment
feature toggles. The prompt explicitly requires environment-specific config
and secrets handling.

## Decision

We use **Viper** with a strict layering (lowest precedence first):

1. Built-in defaults baked into the service.
2. `/etc/vibesync/<service>.yaml` (container path) and
   `<service>.<env>.yaml` overlay.
3. `./config/<service>.<env>.yaml` (local dev).
4. Environment variables prefixed `VB_`, where `.`/`-` map to `_`
   (`VB_DATABASE_PRIMARY_HOST` → `database.primary.host`).
5. Explicit overrides (CLI flags, tests).

**Env always wins** so deployments can override file values without
rebuilding. **Secrets come only from env** — never from files committed to
the repo. This is the 12-factor contract.

Port: `libs/config.Loader`. Each service unmarshals into a typed struct
rather than reading values piecemeal.

## Consequences

- **Pros:** uniform across services; env-first fits containers; no secrets
  in files; typed structs catch config errors at startup.
- **Cons:** the `.yaml` overlay convention requires a small amount of
  ceremony; missing files are not errors (env may fully specify config),
  which can mask misconfiguration early. Mitigated by a startup validation
  step in each service's main.

## Alternatives considered

- **Pure env vars (no files):** workable but verbose for nested config and
  hostile to local-dev defaults. Rejected as the sole mechanism.
- **etcd/Consul dynamic config:** valuable for hot-reload but adds an
  operational dependency; not needed in Phase 1. May be revisited if feature
  flags require runtime mutation.
