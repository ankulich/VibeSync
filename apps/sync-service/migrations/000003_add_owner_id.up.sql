-- Owner-primary timing (ADR sync algorithm revision): the room remembers its
-- owner so the owner is the drift-correction reference and reclaims the host
-- role whenever present. Backfill from host_id: for rooms created after this
-- change Init sets the real owner; older rooms at best hosted their owner
-- anyway, and an unknown owner simply keeps the previous host-only behavior.
ALTER TABLE sync_state ADD COLUMN owner_id VARCHAR(26) NOT NULL DEFAULT '';
UPDATE sync_state SET owner_id = host_id WHERE owner_id = '';
