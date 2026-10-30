-- Events read from PostgreSQL server logs by the log agent (`locklens
-- agent`), which runs on the database host: slow statements and their plans
-- (auto_explain), lock waits, deadlocks, autovacuum, temporary files,
-- checkpoints, and errors. Statement texts are normalised and messages
-- redacted by the agent before they are sent. event_id identifies a log
-- message, so a batch sent twice is stored once.
CREATE TABLE log_events (
    target_id    bigint           NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    ts           timestamptz      NOT NULL,
    event_id     text             NOT NULL,
    kind         text             NOT NULL,
    pid          integer,
    datname      text,
    username     text,
    application  text,
    severity     text             NOT NULL,
    sqlstate     text,
    duration_ms  double precision,
    query_id     bigint,
    fingerprint  text,
    query        text,
    message      text             NOT NULL,
    details      jsonb            NOT NULL DEFAULT '{}',
    PRIMARY KEY (target_id, ts, event_id)
);
CREATE INDEX log_events_kind ON log_events (target_id, kind, ts);
CREATE INDEX log_events_fingerprint ON log_events (target_id, fingerprint, ts) WHERE fingerprint IS NOT NULL;
CREATE INDEX log_events_ts ON log_events (ts);

-- The log agents that sent events, one per token.
CREATE TABLE log_agents (
    token_id      bigint      PRIMARY KEY REFERENCES api_tokens (id) ON DELETE CASCADE,
    target_id     bigint      NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    hostname      text        NOT NULL,
    version       text        NOT NULL,
    sources       jsonb       NOT NULL DEFAULT '[]',
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL,
    last_event_at timestamptz,
    received      bigint      NOT NULL DEFAULT 0
);
