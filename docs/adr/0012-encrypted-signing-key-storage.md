# ADR-0012: Encrypted JWT signing keys in Postgres

- **Status:** Accepted
- **Date:** 2026-07-27
- **Phase:** 4 (Auth Service)

## Context

The Auth Service signs and verifies its own JWTs, so it must hold its signing
keypair. Where the private key lives matters: it has to survive deploys and
restarts, support rotation without a code change, and not become plaintext at
rest if the database is compromised.

## Decision

JWT signing keys are **stored encrypted at rest in the `auth.signing_keys`
Postgres table**, with rotation and hot-reload built in.

### Encryption

- **Private side:** AES-256-GCM encrypted with a master key from
  `VB_AUTH_KEY_MASTER` (env var, base64-encoded 32 bytes).
- **Public side:** stored as a canonical JWK and served verbatim via
  `GetJwks` so any verifier can fetch the public key.

### Rotation

1. A new keypair is generated.
2. The private key is encrypted and inserted as **active**.
3. The previously active key is marked **retired**.
4. There is **exactly one active key**, enforced by a partial unique index on
   `state = 'active'`.
5. Retired keys keep **verifying** until their issued tokens expire naturally;
   only the active key **signs**.

### Hot reload

`Reload()` hot-swaps the in-memory signer from the table without a process
restart, so rotation takes effect immediately.

### Bootstrap

On first startup against an empty `auth.signing_keys` table, the service
generates a fresh keypair and inserts it as active.

- **Production MUST set `VB_AUTH_KEY_MASTER`.**
- If the env var is absent, the service generates an **ephemeral** master key
  and logs a warning. Tokens do not survive a restart in this mode — it is
  dev-only.

### Why not on-disk key files

- Requires file operations on every rotation and deployment.
- Harder to audit who changed what and when.

### Why not in-memory only

- Tokens do not survive deploys or restarts — every restart invalidates all
  sessions.

## Consequences

- **Pros:** keys survive deploys; rotation is a row insert, not a deploy;
  verification stays continuous through rotation; encrypted-at-rest survives a
  DB backup leak (the attacker still needs the master key).
- **Cons:** **the master key is the one secret to protect.** Losing it makes
  every signing key unrecoverable — all outstanding tokens become unverifiable
  and must be reissued after a fresh keypair. Mitigated by treating
  `VB_AUTH_KEY_MASTER` as a tier-1 secret in the secret manager.
- **Trade-off accepted:** we accept the operational centrality of one master
  key in exchange for stateless-friendly, auditable, encrypted-at-rest key
  storage.

## Alternatives considered

- **On-disk PEM files mounted as secrets:** operationally heavier to rotate,
  weaker auditability. Rejected.
- **In-memory keypairs generated at boot:** simplest, but tokens die on every
  deploy. Rejected for production.
- **External KMS / HSM:** strongest boundary, but adds a network dependency and
  a new failure mode on the JWT hot path. Reconsider when threat model or
  compliance demands it.
