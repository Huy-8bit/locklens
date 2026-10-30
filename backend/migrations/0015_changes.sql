-- Changes on monitored instances: detected ones (settings, restarts,
-- promotions, upgrades, schema) and reported ones (deploys, migrations).
-- They are drawn on charts and weighed as possible explanations of
-- anomalies. target_id NULL means a change that concerns every instance.
CREATE TABLE change_events (
    id          bigserial   PRIMARY KEY,
    target_id   bigint      REFERENCES targets (id) ON DELETE CASCADE,
    at          timestamptz NOT NULL,
    kind        text        NOT NULL,
    source      text        NOT NULL CHECK (source IN ('detected', 'api', 'user')),
    title       text        NOT NULL,
    details     jsonb       NOT NULL DEFAULT '{}',
    created_by  text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX change_events_target_at ON change_events (target_id, at);
CREATE INDEX change_events_at ON change_events (at);

-- The last reading each change detection compares against (the server's
-- settings and facts, the schema of each database).
CREATE TABLE change_state (
    target_id   bigint      NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    kind        text        NOT NULL,
    observed_at timestamptz NOT NULL,
    state       jsonb       NOT NULL,
    PRIMARY KEY (target_id, kind)
);

-- Tokens for machines: deploy pipelines report changes ("events"), log
-- agents ship log events ("ingest"). Only a SHA-256 of the token is kept;
-- the token is shown once when it is created.
CREATE TABLE api_tokens (
    id           bigserial   PRIMARY KEY,
    name         text        NOT NULL,
    scope        text        NOT NULL CHECK (scope IN ('events', 'ingest')),
    target_id    bigint      REFERENCES targets (id) ON DELETE CASCADE,
    token_hash   bytea       NOT NULL UNIQUE,
    prefix       text        NOT NULL,
    created_by   text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at   timestamptz
);
