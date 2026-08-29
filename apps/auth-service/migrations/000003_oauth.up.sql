-- VibeSync Auth Service — migration 0003: OAuth flows, OAuth account links, outbox.
-- See ADR-0004 (transactional outbox), ADR-0010 (user upsert on OAuth login).

-- --- oauth_flows --------------------------------------------------------
-- Transient state for in-progress Authorization Code + PKCE flows.
-- Rows are short-lived (oauth_flow_ttl, default 10m) and single-use.
CREATE TABLE auth.oauth_flows (
    state           VARCHAR(32)   PRIMARY KEY,
    provider        VARCHAR(32)   NOT NULL,
    redirect_uri    TEXT          NOT NULL,
    code_challenge  VARCHAR(64)   NOT NULL DEFAULT '',
    user_agent      TEXT          NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ   NOT NULL
);

CREATE INDEX oauth_flows_expires_at_idx ON auth.oauth_flows (expires_at);

-- --- oauth_accounts -----------------------------------------------------
-- Durable links between VibeSync users and provider identities.
-- One user may have links to multiple providers; a (provider, provider_user_id)
-- pair links to exactly one user.
CREATE TABLE auth.oauth_accounts (
    provider           VARCHAR(32)   NOT NULL,
    provider_user_id   VARCHAR(255)  NOT NULL,
    user_id            CHAR(26)      NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    created_at         TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, provider_user_id)
);

CREATE UNIQUE INDEX oauth_accounts_user_provider_idx
    ON auth.oauth_accounts (user_id, provider);

-- --- outbox -------------------------------------------------------------
-- Transactional outbox: events staged in the same tx as the domain write,
-- drained to Kafka by the relay. See libs/outbox and ADR-0004.
CREATE TABLE auth.outbox (
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

-- Relay polls unpublished rows ordered by occurrence time for FIFO per aggregate.
CREATE INDEX outbox_unpublished_idx
    ON auth.outbox (occurred_at)
    WHERE published = FALSE;
