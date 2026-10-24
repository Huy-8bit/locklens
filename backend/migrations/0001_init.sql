-- LockLens monitoring store, schema version 1.
--
-- This database holds LockLens' own data: the admin account, sessions,
-- monitored targets (with encrypted connection strings), collected history,
-- findings, and incidents. It must never be the database being monitored.
--
-- All timestamps are timestamptz and written in UTC.

CREATE TABLE admin_users (
    id            bigserial PRIMARY KEY,
    username      text        NOT NULL UNIQUE,
    password_hash text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Server-side sessions. Only a SHA-256 hash of the cookie token is stored.
CREATE TABLE auth_sessions (
    token_hash   bytea PRIMARY KEY,
    user_id      bigint      NOT NULL REFERENCES admin_users (id) ON DELETE CASCADE,
    csrf_token   text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX auth_sessions_expires ON auth_sessions (expires_at);

-- Monitored PostgreSQL instances. The DSN is AES-256-GCM encrypted with the
-- master key from LOCKLENS_MASTER_KEY, which is never stored here. The
-- display_* columns hold the non-secret parts shown in the UI.
CREATE TABLE targets (
    id                 bigserial PRIMARY KEY,
    name               text        NOT NULL,
    description        text        NOT NULL DEFAULT '',
    dsn_ciphertext     bytea       NOT NULL,
    display_host       text        NOT NULL,
    display_port       integer     NOT NULL,
    display_database   text        NOT NULL,
    display_user       text        NOT NULL,
    enabled            boolean     NOT NULL DEFAULT true,
    interval_seconds   integer     NOT NULL DEFAULT 10 CHECK (interval_seconds BETWEEN 5 AND 3600),
    query_text_enabled boolean     NOT NULL DEFAULT false,
    max_connections    integer     NOT NULL DEFAULT 2 CHECK (max_connections BETWEEN 1 AND 5),
    node_exporter_url  text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    -- Soft delete: collection stops and the target is hidden; history stays
    -- until retention removes it or the target is purged explicitly.
    deleted_at         timestamptz
);
CREATE UNIQUE INDEX targets_name_unique ON targets (lower(name)) WHERE deleted_at IS NULL;

CREATE TABLE target_status (
    target_id          bigint PRIMARY KEY REFERENCES targets (id) ON DELETE CASCADE,
    state              text        NOT NULL,
    server_version     text,
    server_version_num integer,
    is_standby         boolean,
    capabilities       jsonb       NOT NULL DEFAULT '{}',
    collectors         jsonb       NOT NULL DEFAULT '{}',
    last_attempt_at    timestamptz,
    last_success_at    timestamptz,
    last_error         text,
    last_error_at      timestamptz,
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- Time series. A NULL value means "collected but unknown" (for example a rate
-- across a statistics reset); a missing row means "not collected".
CREATE TABLE metric_points (
    target_id bigint           NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    metric    text             NOT NULL,
    label     text             NOT NULL DEFAULT '',
    ts        timestamptz      NOT NULL,
    value     double precision,
    PRIMARY KEY (target_id, metric, label, ts)
);
CREATE INDEX metric_points_ts ON metric_points (ts);

-- pg_stat_statements entries. Identity includes the server major version
-- because queryid is not stable across major versions, and database, user,
-- and toplevel because the same queryid can appear under each.
CREATE TABLE query_identities (
    id           bigserial PRIMARY KEY,
    target_id    bigint      NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    server_major integer     NOT NULL,
    dbid         oid         NOT NULL,
    userid       oid         NOT NULL,
    toplevel     boolean     NOT NULL,
    queryid      bigint      NOT NULL,
    datname      text        NOT NULL,
    username     text        NOT NULL,
    -- Normalised text from pg_stat_statements, only when the target opted in.
    query_text   text,
    first_seen   timestamptz NOT NULL,
    last_seen    timestamptz NOT NULL,
    UNIQUE (target_id, server_major, dbid, userid, toplevel, queryid)
);
CREATE INDEX query_identities_last_seen ON query_identities (last_seen);

-- Per-collection deltas for identities whose counters changed.
CREATE TABLE query_samples (
    target_id         bigint           NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    identity_id       bigint           NOT NULL REFERENCES query_identities (id) ON DELETE CASCADE,
    ts                timestamptz      NOT NULL,
    interval_seconds  double precision NOT NULL,
    calls             bigint           NOT NULL,
    total_exec_ms     double precision NOT NULL,
    rows              bigint           NOT NULL,
    shared_blks_hit   bigint           NOT NULL,
    shared_blks_read  bigint           NOT NULL,
    temp_blks_written bigint           NOT NULL,
    wal_bytes         numeric,
    PRIMARY KEY (target_id, identity_id, ts)
);
CREATE INDEX query_samples_target_ts ON query_samples (target_id, ts);
CREATE INDEX query_samples_ts ON query_samples (ts);

-- One row per pg_stat_statements collection, for coverage and reset tracking.
CREATE TABLE statement_collections (
    target_id         bigint           NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    ts                timestamptz      NOT NULL,
    interval_seconds  double precision,
    entries           integer          NOT NULL,
    changed           integer          NOT NULL,
    hidden            integer          NOT NULL,
    dealloc_delta     bigint,
    reset_detected    boolean          NOT NULL DEFAULT false,
    PRIMARY KEY (target_id, ts)
);

-- Activity snapshots (model.Snapshot schema v1 as JSON), stored when there is
-- blocking or a long transaction, and periodically otherwise.
CREATE TABLE activity_snapshots (
    id                bigserial PRIMARY KEY,
    target_id         bigint      NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    ts                timestamptz NOT NULL,
    blocked_sessions  integer     NOT NULL,
    root_blockers     integer     NOT NULL,
    long_transactions integer     NOT NULL,
    snapshot          jsonb       NOT NULL
);
CREATE INDEX activity_snapshots_target_ts ON activity_snapshots (target_id, ts DESC);
CREATE INDEX activity_snapshots_ts ON activity_snapshots (ts);

CREATE TABLE table_stats (
    target_id           bigint      NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    ts                  timestamptz NOT NULL,
    relid               oid         NOT NULL,
    datname             text        NOT NULL,
    schemaname          text        NOT NULL,
    relname             text        NOT NULL,
    n_live_tup          bigint,
    n_dead_tup          bigint,
    n_mod_since_analyze bigint,
    last_vacuum         timestamptz,
    last_autovacuum     timestamptz,
    last_analyze        timestamptz,
    last_autoanalyze    timestamptz,
    vacuum_count        bigint,
    autovacuum_count    bigint,
    analyze_count       bigint,
    autoanalyze_count   bigint,
    table_bytes         bigint,
    indexes_bytes       bigint,
    total_bytes         bigint,
    frozenxid_age       bigint,
    PRIMARY KEY (target_id, ts, relid)
);
CREATE INDEX table_stats_ts ON table_stats (ts);

CREATE TABLE index_stats (
    target_id     bigint      NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    ts            timestamptz NOT NULL,
    indexrelid    oid         NOT NULL,
    relid         oid         NOT NULL,
    schemaname    text        NOT NULL,
    relname       text        NOT NULL,
    indexrelname  text        NOT NULL,
    idx_scan      bigint,
    index_bytes   bigint,
    is_unique     boolean     NOT NULL,
    is_primary    boolean     NOT NULL,
    is_constraint boolean     NOT NULL,
    PRIMARY KEY (target_id, ts, indexrelid)
);
CREATE INDEX index_stats_ts ON index_stats (ts);

-- Structured state that is shown as a whole (replication, vacuum progress,
-- settings), one JSON document per collection.
CREATE TABLE state_snapshots (
    target_id bigint      NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    kind      text        NOT NULL,
    ts        timestamptz NOT NULL,
    payload   jsonb       NOT NULL,
    PRIMARY KEY (target_id, kind, ts)
);
CREATE INDEX state_snapshots_ts ON state_snapshots (ts);

-- Periods in which a collector did not produce data.
CREATE TABLE collection_gaps (
    id         bigserial PRIMARY KEY,
    target_id  bigint      NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    collector  text        NOT NULL,
    started_at timestamptz NOT NULL,
    ended_at   timestamptz NOT NULL,
    reason     text        NOT NULL
);
CREATE INDEX collection_gaps_target ON collection_gaps (target_id, ended_at DESC);

CREATE TABLE incidents (
    id              bigserial PRIMARY KEY,
    -- Kept when a target is soft-deleted; set to NULL only by an explicit purge
    -- that keeps incidents.
    target_id       bigint REFERENCES targets (id) ON DELETE SET NULL,
    target_name     text        NOT NULL,
    rule_id         text        NOT NULL,
    rule_version    integer     NOT NULL,
    severity        text        NOT NULL,
    title           text        NOT NULL,
    state           text        NOT NULL CHECK (state IN ('open', 'resolved')),
    opened_at       timestamptz NOT NULL,
    last_seen_at    timestamptz NOT NULL,
    resolved_at     timestamptz,
    resolution      text,
    trigger_config  jsonb       NOT NULL,
    acknowledged_at timestamptz,
    acknowledged_by text,
    ack_note        text,
    event_count     integer     NOT NULL DEFAULT 0,
    size_bytes      bigint      NOT NULL DEFAULT 0,
    truncated       boolean     NOT NULL DEFAULT false,
    source          text        NOT NULL DEFAULT 'recorder' CHECK (source IN ('recorder', 'import'))
);
CREATE INDEX incidents_target_opened ON incidents (target_id, opened_at DESC);
CREATE INDEX incidents_opened ON incidents (opened_at DESC);
CREATE INDEX incidents_open ON incidents (target_id, rule_id) WHERE state = 'open';

CREATE TABLE incident_events (
    id          bigserial PRIMARY KEY,
    incident_id bigint      NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    ts          timestamptz NOT NULL,
    kind        text        NOT NULL,
    summary     text        NOT NULL,
    payload     jsonb       NOT NULL DEFAULT '{}'
);
CREATE INDEX incident_events_incident_ts ON incident_events (incident_id, ts, id);

CREATE TABLE findings (
    id                    bigserial PRIMARY KEY,
    target_id             bigint      NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    rule_id               text        NOT NULL,
    rule_version          integer     NOT NULL,
    finding_key           text        NOT NULL,
    severity              text        NOT NULL,
    title                 text        NOT NULL,
    status                text        NOT NULL CHECK (status IN ('active', 'resolved', 'insufficient_data')),
    first_seen            timestamptz NOT NULL,
    last_seen             timestamptz NOT NULL,
    resolved_at           timestamptz,
    observed              jsonb       NOT NULL DEFAULT '[]',
    evidence              jsonb       NOT NULL DEFAULT '[]',
    explanation           text        NOT NULL,
    possible_explanations jsonb       NOT NULL DEFAULT '[]',
    next_checks           jsonb       NOT NULL DEFAULT '[]',
    limitations           text        NOT NULL,
    incident_id           bigint REFERENCES incidents (id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX findings_open_unique ON findings (target_id, rule_id, finding_key) WHERE status <> 'resolved';
CREATE INDEX findings_target_last_seen ON findings (target_id, last_seen DESC);

CREATE TABLE settings (
    key        text PRIMARY KEY,
    value      jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
