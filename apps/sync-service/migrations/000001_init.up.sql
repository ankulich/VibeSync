-- VibeSync Sync Service — migration 0001: sync_state + outbox.

-- --- sync_state ---------------------------------------------------------
-- The authoritative playback state per room. Persisted for restart recovery;
-- the in-memory RoomSync is the live source of truth, this is the durable
-- checkpoint written periodically.
CREATE TABLE sync_state (
    room_id        CHAR(26)      PRIMARY KEY,
    media_id       CHAR(26)      NOT NULL DEFAULT '',
    status         SMALLINT      NOT NULL DEFAULT 1,
    media_time_ms  BIGINT        NOT NULL DEFAULT 0,
    wall_time_ms   BIGINT        NOT NULL DEFAULT 0,
    playback_rate  DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    epoch          BIGINT        NOT NULL DEFAULT 0,
    host_id        CHAR(26)      NOT NULL DEFAULT '',
    fencing_token  BIGINT        NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ   NOT NULL DEFAULT now()
);

-- --- outbox -------------------------------------------------------------
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
