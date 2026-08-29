-- VibeSync Media Service — migration 0001: media_items + media_queue + outbox.

-- --- media_items ---------------------------------------------------------
CREATE TABLE media_items (
    id            CHAR(26)      PRIMARY KEY,
    kind          SMALLINT      NOT NULL DEFAULT 1,
    source        SMALLINT      NOT NULL DEFAULT 1,
    external_ref  VARCHAR(255)  NOT NULL DEFAULT '',
    title         VARCHAR(255)  NOT NULL,
    artist        VARCHAR(255)  NOT NULL DEFAULT '',
    duration_ms   BIGINT        NOT NULL DEFAULT 0,
    cover_url     TEXT          NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE INDEX media_items_created_at_idx ON media_items (created_at DESC);
CREATE INDEX media_items_title_trgm_idx ON media_items USING gin (title gin_trgm_ops);

-- --- media_queue ---------------------------------------------------------
-- Per-room ordered queue of media items. Position starts at 0 (the currently
-- playing item). NEXT advances position; PREVIOUS decrements.
CREATE TABLE media_queue (
    room_id    CHAR(26)    NOT NULL,
    position   INTEGER     NOT NULL,
    media_id   CHAR(26)    NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    added_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, position)
);

CREATE INDEX media_queue_room_idx ON media_queue (room_id, position);

-- --- outbox --------------------------------------------------------------
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
