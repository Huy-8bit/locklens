-- Routing and escalation of notifications, Alertmanager channels, and
-- maintenance windows.

-- A channel may be limited to some rules and instances (empty: all), and
-- may receive an incident only once it stayed open and unacknowledged for
-- delay_seconds (an escalation tier).
ALTER TABLE notification_channels
    ADD COLUMN rule_ids      text[]   NOT NULL DEFAULT '{}',
    ADD COLUMN target_ids    bigint[] NOT NULL DEFAULT '{}',
    ADD COLUMN delay_seconds integer  NOT NULL DEFAULT 0 CHECK (delay_seconds >= 0);
ALTER TABLE notification_channels DROP CONSTRAINT notification_channels_kind_check;
ALTER TABLE notification_channels ADD CONSTRAINT notification_channels_kind_check
    CHECK (kind IN ('webhook', 'slack', 'email', 'alertmanager'));

-- Periods in which notifications are not sent (incidents are still
-- recorded): once, every week, or after each reported deploy or migration
-- of the instances they cover.
CREATE TABLE maintenance_windows (
    id               bigserial   PRIMARY KEY,
    name             text        NOT NULL,
    enabled          boolean     NOT NULL DEFAULT true,
    kind             text        NOT NULL CHECK (kind IN ('once', 'weekly', 'after_deploy')),
    environments     text[]      NOT NULL DEFAULT '{}',
    target_ids       bigint[]    NOT NULL DEFAULT '{}',
    rule_ids         text[]      NOT NULL DEFAULT '{}',
    starts_at        timestamptz,
    ends_at          timestamptz,
    weekdays         integer[]   NOT NULL DEFAULT '{}',
    start_time       text        NOT NULL DEFAULT '',
    duration_minutes integer     NOT NULL DEFAULT 0,
    timezone         text        NOT NULL DEFAULT 'UTC',
    created_by       text        NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- API tokens for Prometheus (and other scrapers) and for reading the API.
ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_scope_check;
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_scope_check CHECK (scope IN ('events', 'ingest', 'metrics', 'read'));
