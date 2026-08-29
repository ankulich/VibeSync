CREATE TABLE resolution_cache (
    provider      VARCHAR(32)  NOT NULL,
    external_ref  VARCHAR(255) NOT NULL,
    title         VARCHAR(255) NOT NULL,
    artist        VARCHAR(255) NOT NULL DEFAULT '',
    cover_url     TEXT         NOT NULL DEFAULT '',
    duration_ms   BIGINT       NOT NULL DEFAULT 0,
    resolved_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, external_ref)
);
