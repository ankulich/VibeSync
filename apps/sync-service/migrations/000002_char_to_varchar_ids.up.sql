-- CHAR(26) columns blank-pad empty strings to 26 spaces, so rooms without
-- loaded media persisted media_id = '<26 spaces>' and republished it in every
-- snapshot (clients then queried media by a whitespace id). Switch to
-- VARCHAR, which preserves '', and trim what already leaked in.
UPDATE sync_state SET media_id = TRIM(BOTH FROM media_id);
UPDATE sync_state SET host_id = TRIM(BOTH FROM host_id);
ALTER TABLE sync_state ALTER COLUMN media_id TYPE VARCHAR(26);
ALTER TABLE sync_state ALTER COLUMN host_id TYPE VARCHAR(26);
