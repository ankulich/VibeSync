-- VibeSync Auth Service — migration 0002: refresh tokens + reuse-detection.
-- See ADR-0011 (rotation + reuse detection).
--
-- Storage model:
--   selector      : plaintext, indexed UNIQUE — O(1) lookup at refresh time
--   validator_hash: argon2 hash of the validator half — never the plaintext
--   family_id     : groups tokens from one login; family-wide revocation key
--
-- Reuse detection relies on the status column. A USED token presented again
-- triggers RevokeFamily (status -> compromised for every token in the family).

CREATE TABLE auth.refresh_tokens (
    id              CHAR(26)      PRIMARY KEY,
    family_id       CHAR(26)      NOT NULL,
    user_id         CHAR(26)      NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    session_id      CHAR(26)      NOT NULL REFERENCES auth.sessions(id) ON DELETE CASCADE,
    selector        VARCHAR(32)   NOT NULL UNIQUE,
    validator_hash  TEXT          NOT NULL,
    rotated_from    CHAR(26),
    status          SMALLINT      NOT NULL DEFAULT 1 CHECK (status BETWEEN 0 AND 5),
    expires_at      TIMESTAMPTZ   NOT NULL,
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    used_at         TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ
);

-- Selector is the only lookup key at refresh time; the UNIQUE constraint above
-- already indexes it. The indexes below support reuse detection and listing.
CREATE INDEX refresh_tokens_family_idx ON auth.refresh_tokens (family_id);
CREATE INDEX refresh_tokens_user_idx   ON auth.refresh_tokens (user_id, created_at DESC);
