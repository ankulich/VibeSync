# VibeSync Auth Service

Operational and developer reference for the Auth Service. For architecture
decisions, see `docs/adr/0010` through `docs/adr/0013`. For the wire
contracts, see `proto/vibesync/auth/v1/auth.proto`.

## What it does

The Auth Service owns:

- **User identity** (Phase 4 scope): the `auth.users` table is the canonical
  record. The User Service (Phase 5) builds its read model from the
  `user.created.v1` event.
- **Password authentication**: argon2id hashing, constant-time comparison.
- **OAuth2 login**: Authorization Code + PKCE via Spotify and Google.
- **Session management**: per-device sessions that group refresh-token families.
- **Refresh tokens**: single-use, rotating, with reuse detection (ADR-0011).
- **JWT signing keys**: encrypted at rest, hot-rotatable (ADR-0012).
- **Token introspection**: `Introspect` for services that want a definitive
  active/inactive answer.

## Token model

| Token | Lifetime | Storage | Revocation |
|-------|----------|---------|------------|
| Access (JWT) | 15 min (config: `auth.access_ttl`) | Stateless; not persisted | Expires naturally; no server-side revocation |
| Refresh | 30 days (config: `auth.refresh_ttl`) | `auth.refresh_tokens` table; selector plaintext, validator argon2-hashed | Single-use rotation; logout; reuse → family burn |

The access token is RS256-signed JWT with claims: `sub` (user id), `role`
(SystemRole numeric), `iss`, `aud`, `iat`, `exp`, `jti`. Verification requires
the JWKS (served by `GetJwks`) or a call to `Introspect`.

## Refresh-token rotation + reuse detection

The most security-critical logic. See ADR-0011 for full rationale.

```
client presents "<selector>.<validator>"
  → lookup by selector (O(1), indexed)
  → verify validator hash (constant-time)
  → classify by status:
       active     → rotate: mark used, issue new in same family
       used       → REUSE: revoke entire family, return REUSE_DETECTED
       revoked    → return REVOKED
       compromised → return FAMILY_COMPROMISED
       (expired)  → return EXPIRED
```

All of the above runs in one Postgres transaction. A reuse event is loud: the
entire family is burned and the legitimate client must re-authenticate. This
is intentional — a token that's been presented twice has been captured, and
containing the compromise outweighs the user friction.

## Key management

Signing keys live in `auth.signing_keys`:

- `kid` (TEXT PK), `status` (SMALLINT: 1=active, 2=retired),
  `private_encrypted` (BYTEA, AES-256-GCM), `public_jwk` (JSONB), timestamps.
- Exactly one active key — enforced by a partial unique index.
- Rotation: `RotateKeys` RPC or auto-rotation on `auth.key_rotation_interval`.
  New keypair → encrypt private → insert as active → mark previous retired →
  `Reload()` the in-memory signer.

The master key comes from `VB_AUTH_KEY_MASTER` env (base64-encoded 32 bytes).
**If absent, the service generates an ephemeral one and logs a warning** —
this is dev-only; tokens will not survive a restart. Production MUST set the
env var. Generate one:

```bash
openssl rand -base64 32
```

## OAuth provider setup

Each provider is gated by `oauth.<provider>.enabled` in config. When enabled,
the service reads credentials from env at startup:

| Provider | Client ID env | Client Secret env | Scopes |
|----------|---------------|-------------------|--------|
| Spotify  | `VB_OAUTH_SPOTIFY_CLIENT_ID` | `VB_OAUTH_SPOTIFY_CLIENT_SECRET` | `user-read-email user-read-private` |
| Google   | `VB_OAUTH_GOOGLE_CLIENT_ID` | `VB_OAUTH_GOOGLE_CLIENT_SECRET` | `openid email profile` |

The OAuth callback URL is `<oauth.redirect_uri_base>/api/v1/oauth/callback`.
Register this URL in your provider's app console. The flow:

1. Client → `BeginOAuth(provider, redirect_uri)` → returns `authorization_url` + `state`.
2. Client redirects browser to `authorization_url`.
3. Provider redirects back to the callback URL with `code` + `state`.
4. Client → `CompleteOAuth(provider, code, state)` → returns `AuthSession` (tokens + user id).

On first OAuth login for a given provider identity, Auth creates a new user
and emits `user.created.v1` to Kafka (via the outbox). Subsequent logins
reuse the existing user and link.

## Running locally

```bash
# 1. Start the infra stack (Postgres, Redis, Kafka).
make docker-up

# 2. Set required env (secrets).
export VB_DB_PASSWORD=vibesync
export VB_AUTH_KEY_MASTER=$(openssl rand -base64 32)  # or reuse a stable one

# 3. (Optional) Enable OAuth providers.
export VB_OAUTH_SPOTIFY_ENABLED=true  # via config or VB_ env override
export VB_OAUTH_SPOTIFY_CLIENT_ID=...
export VB_OAUTH_SPOTIFY_CLIENT_SECRET=...

# 4. Run the service.
go run ./apps/auth-service/cmd/auth-service
```

The service listens on `:8080` (config: `web.addr`). Endpoints:

- `POST /vibesync.auth.v1.AuthService/Login` (Connect, HTTP/JSON or gRPC)
- `POST /vibesync.auth.v1.AuthService/RefreshToken`
- ... (all 8 RPCs)
- `GET /healthz` — always 200
- `GET /readyz` — 200 after deps warm, 503 during startup
- `GET /metrics` — Prometheus

## Schema

Migrations live in `apps/auth-service/migrations/` and are embedded into the
binary (run automatically at startup). Tables (schema `auth`):

- `users` — canonical user records
- `sessions` — per-device sessions
- `refresh_tokens` — selector/validator, family, status
- `signing_keys` — encrypted JWT keys
- `oauth_flows` — transient PKCE state
- `oauth_accounts` — user ↔ provider links
- `outbox` — transactional outbox for events

## Testing

```bash
# Unit tests (no Docker).
make test

# Integration tests (require Docker for testcontainers).
VIBE_TEST_INTEGRATION=1 go test -tags=integration -count=1 ./internal/infra/postgres/...
```

Unit tests cover: user validation, the refresh-token state machine (every
transition), password hashing round-trip, key cipher round-trip, JWT
sign/verify/tamper/expiry/rotation, OAuth profile mapping.
