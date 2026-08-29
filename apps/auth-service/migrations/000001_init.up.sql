-- VibeSync Auth Service — migration 0001: schema, users, sessions, signing keys.
-- See ADR-0010 (auth owns users), ADR-0012 (encrypted signing keys).
--
-- All Auth-owned tables live in the `auth` schema. The connection's search_path
-- is set per role; queries in repositories schema-qualify explicitly to avoid
-- ambiguity.

CREATE SCHEMA IF NOT EXISTS auth;

-- citext for case-insensitive email matching. Available in PG 16 by default;
-- requires the extension to be installed (superuser at init). The compose
-- init runs as the vibesync user which can CREATE EXTENSION in its own DB.
CREATE EXTENSION IF NOT EXISTS citext;

-- --- users --------------------------------------------------------------
-- A row per VibeSync user. Auth is the canonical owner (ADR-0010); the User
-- Service read-model is populated from user.created.v1.
CREATE TABLE auth.users (
    id            CHAR(26)      PRIMARY KEY,
    email         CITEXT        NOT NULL UNIQUE,
    username      VARCHAR(32)   NOT NULL UNIQUE,
    display_name  VARCHAR(100)  NOT NULL,
    avatar_url    TEXT          NOT NULL DEFAULT '',
    -- argon2id hash; empty string for OAuth-only users (login via provider).
    password_hash TEXT          NOT NULL DEFAULT '',
    -- matches vibesync.common.v1.SystemRole numeric values.
    system_role   SMALLINT      NOT NULL DEFAULT 2 CHECK (system_role BETWEEN 0 AND 4),
    status        SMALLINT      NOT NULL DEFAULT 1 CHECK (status BETWEEN 0 AND 3),
    created_at    TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ   NOT NULL DEFAULT now()
);

-- Most-recent-first lookups for "users" lists and audit.
CREATE INDEX users_created_at_idx ON auth.users (created_at DESC);

-- --- sessions -----------------------------------------------------------
-- A session = a logged-in device. Groups the refresh-token family. Survives
-- refresh rotations; deleted on full logout.
CREATE TABLE auth.sessions (
    id            CHAR(26)      PRIMARY KEY,
    user_id       CHAR(26)      NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    device_label  VARCHAR(100)  NOT NULL DEFAULT '',
    family_id     CHAR(26)      NOT NULL UNIQUE,
    created_at    TIMESTAMPTZ   NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ   NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ   NOT NULL,
    revoked_at    TIMESTAMPTZ
);

CREATE INDEX sessions_user_id_idx ON auth.sessions (user_id);
-- List a user's active sessions, most-recent-first.
CREATE INDEX sessions_user_active_idx ON auth.sessions (user_id, last_seen_at DESC)
    WHERE revoked_at IS NULL;

-- --- signing_keys -------------------------------------------------------
-- JWT signing keys. Private side AES-GCM-encrypted at rest (ADR-0012).
CREATE TABLE auth.signing_keys (
    kid                TEXT          PRIMARY KEY,
    status             SMALLINT      NOT NULL CHECK (status BETWEEN 0 AND 2),
    private_encrypted  BYTEA         NOT NULL,
    public_jwk         JSONB         NOT NULL,
    created_at         TIMESTAMPTZ   NOT NULL DEFAULT now(),
    retired_at         TIMESTAMPTZ
);

-- Exactly one active key enforced by a partial unique index.
CREATE UNIQUE INDEX signing_keys_one_active_idx
    ON auth.signing_keys ((1))
    WHERE status = 1;
