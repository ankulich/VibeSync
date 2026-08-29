# ADR-0011: Refresh token rotation with family-wide reuse detection

- **Status:** Accepted
- **Date:** 2026-07-27
- **Phase:** 4 (Auth Service)

## Context

Long-lived refresh tokens are a theft vector. A stolen token that is valid for
days lets an attacker impersonate the user until expiry. We need a refresh
scheme that limits a stolen token to one use and turns any reuse into a loud,
contained incident rather than a silent compromise.

## Decision

Refresh tokens use a **selector/validator split**, are **single-use with
rotation**, and **reuse of a rotated token triggers family-wide revocation**.

### Format and storage

- **Presented format:** `<selector>.<validator>`, where both halves are random
  bytes encoded as base64url.
- **Database stores the selector plaintext**, indexed for O(1) lookup by
  selector on refresh.
- **The validator is stored as an argon2 hash**, never plaintext. On refresh we
  look up by selector, then argon2-compare the validator.

### Rotation and the family concept

Every refresh token belongs to a **family** (rooted at the initial login). A
successful refresh marks the presented token `used` and issues a fresh token in
the *same family* — the chain. The family id is what gets revoked on trouble.

### State machine

All transitions run inside one Postgres transaction:

```
active   -> used        (normal rotation; new token issued in same family)
used     -> compromised (a used token is presented again -> REUSE DETECTED)
active   -> revoked     (explicit logout)
```

On reuse detection, **the entire family is marked compromised** — every active
token in the family is revoked at once. The client is forced to re-authenticate.

### Why selector/validator

- **O(1) lookup** by the indexed selector, without ever storing the validator
  in a form that could be presented.
- **DB breach containment.** If the `auth.refresh_tokens` table leaks, the
  attacker gets selectors and argon2-hashed validators — no usable tokens.
  Reversing argon2 for every row is infeasible.

### Why this defeats theft

An attacker who captures a token gets exactly one use. When the legitimate
client next refreshes, the original token is already `used`, so the client's
presented (stale) token resolves to REUSE — the chain break is detected, and
the whole family burns. The attacker is ejected and the user re-authenticates.

## Consequences

- **Pros:** stolen tokens yield at most one use; reuse is loud and contained;
  DB compromise does not by itself yield live tokens.
- **Cons:** refresh is a write transaction (mark used + insert new + emit), not
  a cheap read. Accepted as the cost of safety.
- **Cons:** a family-wide burn is disruptive to the legitimate user, who is
  forced to log in again. This is the correct failure mode — safe by default.
- **Validation:** tests cover every state-machine transition — active→used,
  used→compromised (reuse), active→revoked (logout), and family-wide
  revocation on reuse.

## Alternatives considered

- **Opaque, non-rotating refresh tokens stored hashed:** simpler, but a stolen
  token stays valid for its full lifetime with no detection signal. Rejected.
- **Rotating tokens without families (per-token burn on reuse):** detects reuse
  of the immediately previous token but cannot tell a stolen-then-rotated chain
  from a legitimate one, so it cannot contain a sustained compromise. Rejected.
- **JWT-based refresh tokens (stateless):** cannot revoke, rotate, or detect
  reuse without server-side state, which defeats the purpose. Rejected.
