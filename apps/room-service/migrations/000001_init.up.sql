-- VibeSync Room Service — migration 0001: rooms, members, invites, outbox.

CREATE EXTENSION IF NOT EXISTS citext;

-- --- rooms -------------------------------------------------------------
CREATE TABLE rooms (
    id            CHAR(26)     PRIMARY KEY,
    slug          VARCHAR(64)  NOT NULL UNIQUE,
    name          VARCHAR(100) NOT NULL,
    description   TEXT         NOT NULL DEFAULT '',
    visibility    SMALLINT     NOT NULL DEFAULT 1,
    owner_id      CHAR(26)     NOT NULL,
    max_members   INTEGER      NOT NULL DEFAULT 50,
    member_count  INTEGER      NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX rooms_created_at_idx ON rooms (created_at DESC);
CREATE INDEX rooms_public_idx ON rooms (created_at DESC) WHERE visibility = 1;

-- --- room_members ------------------------------------------------------
CREATE TABLE room_members (
    room_id    CHAR(26)    NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    user_id    CHAR(26)    NOT NULL,
    role       SMALLINT    NOT NULL DEFAULT 2,
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, user_id)
);

CREATE INDEX room_members_user_idx ON room_members (user_id);

-- --- room_invite -------------------------------------------------------
CREATE TABLE room_invite (
    code       VARCHAR(32)  PRIMARY KEY,
    room_id    CHAR(26)     NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ
);

-- --- outbox ------------------------------------------------------------
CREATE TABLE outbox (
    id             CHAR(26)      PRIMARY KEY,
    aggregate_id   CHAR(26)      NOT NULL,
    topic          VARCHAR(128)  NOT NULL,
    key            VARCHAR(128)  NOT NULL,
    payload        JSONB         NOT NULL,
    headers        JSONB         NOT NULL DEFAULT '{}'::jsonb,
    occurred_at    TIMESTAMPTZ   NOT NULL DEFAULT now(),
    version        VARCHAR(16)   NOT NULL DEFAULT 'v1',
    published      BOOLEAN       NOT NULL DEFAULT FALSE,
    published_at   TIMESTAMPTZ
);

CREATE INDEX outbox_unpublished_idx ON outbox (occurred_at) WHERE published = FALSE;
