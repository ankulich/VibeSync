-- Owner-granted member permissions (ADR-0017), one bitmask row per member.
-- The room owner holds every permission implicitly and has no row.
CREATE TABLE room_permissions (
    room_id     CHAR(26)    NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    user_id     CHAR(26)    NOT NULL,
    permissions SMALLINT    NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, user_id)
);
