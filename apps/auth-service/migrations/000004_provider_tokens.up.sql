-- VibeSync Auth Service — migration 0004: provider tokens.
-- Stores per-user provider access/refresh tokens (encrypted at rest with the
-- master key via KeyCipher). The Provider Service fetches these via the
-- GetProviderToken RPC; auth refreshes expired ones lazily.

CREATE TABLE auth.provider_tokens (
    provider                VARCHAR(32)  NOT NULL,
    user_id                 CHAR(26)     NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    access_token_encrypted  BYTEA        NOT NULL,
    refresh_token_encrypted BYTEA        NOT NULL,
    expires_at              TIMESTAMPTZ  NOT NULL,
    updated_at              TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, user_id)
);

CREATE INDEX provider_tokens_user_idx ON auth.provider_tokens (user_id);
