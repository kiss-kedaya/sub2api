-- 310_upstream_channel_ops.sql
-- Fourth batch for the operations-center upstream monitor: managed upstream
-- channels (login credentials, captcha, announcements) and one shared
-- notification delivery log so every notification path dedupes in one place.
--
-- Secrets are stored as ciphertext produced by the existing SecretEncryptor.
-- No column here ever holds a plaintext password, session token or API key.

CREATE TABLE IF NOT EXISTS upstream_channels (
    id                       BIGSERIAL PRIMARY KEY,
    name                     VARCHAR(120) NOT NULL,
    site_url                 VARCHAR(512) NOT NULL,
    username                 VARCHAR(255) NOT NULL,
    password_cipher          TEXT NOT NULL DEFAULT '',
    access_token_cipher      TEXT NOT NULL DEFAULT '',
    refresh_token_cipher     TEXT NOT NULL DEFAULT '',
    session_expires_at       TIMESTAMPTZ,
    session_error            TEXT NOT NULL DEFAULT '',
    turnstile_enabled        BOOLEAN NOT NULL DEFAULT FALSE,
    captcha_provider         VARCHAR(32) NOT NULL DEFAULT '',
    captcha_api_key_cipher   TEXT NOT NULL DEFAULT '',
    captcha_endpoint         VARCHAR(255) NOT NULL DEFAULT '',
    proxy_url                VARCHAR(512) NOT NULL DEFAULT '',
    sync_announcements       BOOLEAN NOT NULL DEFAULT TRUE,
    status                   VARCHAR(32) NOT NULL DEFAULT 'active',
    last_login_at            TIMESTAMPTZ,
    last_login_error         TEXT NOT NULL DEFAULT '',
    last_sync_at             TIMESTAMPTZ,
    last_sync_error          TEXT NOT NULL DEFAULT '',
    tags                     TEXT NOT NULL DEFAULT '',
    notes                    TEXT NOT NULL DEFAULT '',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_upstream_channels_site_user
    ON upstream_channels (site_url, username);
CREATE INDEX IF NOT EXISTS idx_upstream_channels_status
    ON upstream_channels (status);

CREATE TABLE IF NOT EXISTS upstream_channel_announcements (
    id                 BIGSERIAL PRIMARY KEY,
    channel_id         BIGINT NOT NULL REFERENCES upstream_channels(id) ON DELETE CASCADE,
    source_key         VARCHAR(160) NOT NULL,
    title              TEXT NOT NULL DEFAULT '',
    content            TEXT NOT NULL DEFAULT '',
    notify_mode        VARCHAR(32) NOT NULL DEFAULT '',
    published_at       TIMESTAMPTZ,
    source_updated_at  TIMESTAMPTZ,
    first_seen_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    pushed_at          TIMESTAMPTZ,
    UNIQUE (channel_id, source_key)
);

CREATE INDEX IF NOT EXISTS idx_upstream_channel_announcements_channel
    ON upstream_channel_announcements (channel_id, first_seen_at DESC);

CREATE TABLE IF NOT EXISTS notification_deliveries (
    id           BIGSERIAL PRIMARY KEY,
    channel      VARCHAR(32) NOT NULL,
    event_kind   VARCHAR(64) NOT NULL DEFAULT '',
    dedupe_key   VARCHAR(255) NOT NULL DEFAULT '',
    target       VARCHAR(255) NOT NULL DEFAULT '',
    status       VARCHAR(16) NOT NULL DEFAULT 'sent',
    error        TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notification_deliveries_created
    ON notification_deliveries (created_at DESC);
