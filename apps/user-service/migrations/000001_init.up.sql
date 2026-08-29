-- VibeSync User Service — migration 0001: users read-model table.
-- See ADR-0010 (Auth owns canonical users) and ADR-0014 (User Service owns
-- profile updates).
--
-- This table is a READ MODEL populated from user.created.v1 Kafka events. The
-- Auth Service's auth.users is the system of record; this copy exists for
-- fast profile lookups and listings without cross-service queries.

-- citext for case-insensitive email/username matching (matches Auth's schema).
CREATE EXTENSION IF NOT EXISTS citext;
-- pg_trgm for fuzzy username search in ListUsers.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE users (
    id            CHAR(26)      PRIMARY KEY,
    email         CITEXT        NOT NULL UNIQUE,
    username      VARCHAR(32)   NOT NULL UNIQUE,
    display_name  VARCHAR(100)  NOT NULL,
    avatar_url    TEXT          NOT NULL DEFAULT '',
    -- matches vibesync.common.v1.SystemRole numeric values; default USER(2).
    system_role   SMALLINT      NOT NULL DEFAULT 2,
    created_at    TIMESTAMPTZ   NOT NULL,
    updated_at    TIMESTAMPTZ   NOT NULL
);

-- Most-recent-first listing (ListUsers default order).
CREATE INDEX users_created_at_idx ON users (created_at DESC);
-- Fuzzy search on username for the ListUsers search filter.
CREATE INDEX users_username_trgm_idx ON users USING gin (username gin_trgm_ops);
