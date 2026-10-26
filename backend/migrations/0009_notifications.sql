-- Incident notifications. A channel says where to send (webhook, Slack, or
-- email) and which incidents and events; its address and credentials are a
-- JSON document sealed with the master key and never returned by the API.
CREATE TABLE notification_channels (
    id                bigserial   PRIMARY KEY,
    name              text        NOT NULL UNIQUE,
    kind              text        NOT NULL CHECK (kind IN ('webhook', 'slack', 'email')),
    enabled           boolean     NOT NULL DEFAULT true,
    min_severity      text        NOT NULL DEFAULT 'warning' CHECK (min_severity IN ('info', 'warning', 'critical')),
    -- Environments whose incidents are sent; empty means every environment.
    environments      text[]      NOT NULL DEFAULT '{}',
    on_open           boolean     NOT NULL DEFAULT true,
    on_escalate       boolean     NOT NULL DEFAULT true,
    on_resolve        boolean     NOT NULL DEFAULT true,
    -- Repeat while an incident stays open and unacknowledged; 0 = never.
    reminder_seconds  integer     NOT NULL DEFAULT 0 CHECK (reminder_seconds = 0 OR reminder_seconds >= 600),
    config_ciphertext bytea       NOT NULL,
    -- A non-secret description of the destination, such as a host name.
    display           text        NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- Silences (maintenance windows) stop notifications for one instance, one
-- rule, or both, for a time. Incidents are still recorded.
CREATE TABLE notification_silences (
    id         bigserial   PRIMARY KEY,
    target_id  bigint      REFERENCES targets (id) ON DELETE CASCADE,
    rule_id    text,
    starts_at  timestamptz NOT NULL,
    ends_at    timestamptz NOT NULL CHECK (ends_at > starts_at),
    reason     text        NOT NULL DEFAULT '',
    created_by text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notification_silences_ends ON notification_silences (ends_at);

-- Every notification attempt: what was sent (or silenced, or why it failed).
-- It also prevents sending the same event twice, for example after a restart.
CREATE TABLE notification_deliveries (
    id          bigserial   PRIMARY KEY,
    channel_id  bigint      NOT NULL REFERENCES notification_channels (id) ON DELETE CASCADE,
    incident_id bigint      REFERENCES incidents (id) ON DELETE CASCADE,
    event       text        NOT NULL CHECK (event IN ('opened', 'escalated', 'resolved', 'reminder', 'test')),
    severity    text,
    status      text        NOT NULL CHECK (status IN ('sent', 'failed', 'silenced')),
    attempts    integer     NOT NULL,
    error       text,
    at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notification_deliveries_incident ON notification_deliveries (incident_id, channel_id, at);
CREATE INDEX notification_deliveries_at ON notification_deliveries (at DESC);
