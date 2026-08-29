-- Reverse of 000001.
DROP TABLE IF EXISTS auth.signing_keys;
DROP TABLE IF EXISTS auth.sessions;
DROP TABLE IF EXISTS auth.users;
DROP SCHEMA IF EXISTS auth;
-- citext is shared; leave it.
