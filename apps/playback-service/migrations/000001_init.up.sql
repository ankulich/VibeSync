-- VibeSync Playback Service — migration 0001: playback_state table.
-- Caches the authoritative playback state per room (persisted for restart
-- recovery). The Sync Service is the source of truth; this is the executor's
-- cache + fencing-token checkpoint.

CREATE TABLE playback_state (
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
