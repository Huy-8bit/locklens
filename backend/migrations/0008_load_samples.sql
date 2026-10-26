-- Active session sampling ("database load"). pg_stat_activity is sampled every
-- few seconds; samples are aggregated per target into 15-second buckets.
-- load_buckets records how many samples (ticks) each bucket holds, so the
-- average number of active sessions of a group is sum(samples) / sum(ticks)
-- and a bucket without ticks is a gap, never zero load.
CREATE TABLE load_buckets (
    target_id        bigint           NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    ts               timestamptz      NOT NULL,
    ticks            integer          NOT NULL CHECK (ticks > 0),
    interval_seconds double precision NOT NULL CHECK (interval_seconds > 0),
    PRIMARY KEY (target_id, ts)
);

-- One row per bucket and group of sessions that shared the same wait class
-- and event, backend type, database, user, application, and statement.
-- queryid is pg_stat_activity.query_id (NULL when not computed); it equals
-- the pg_stat_statements queryid of the top-level statement.
CREATE TABLE load_samples (
    target_id        bigint      NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    ts               timestamptz NOT NULL,
    wait_class       text        NOT NULL,
    wait_event       text        NOT NULL DEFAULT '',
    backend_type     text        NOT NULL DEFAULT '',
    dbid             oid,
    datname          text        NOT NULL DEFAULT '',
    userid           oid,
    usename          text        NOT NULL DEFAULT '',
    application_name text        NOT NULL DEFAULT '',
    queryid          bigint,
    samples          integer     NOT NULL CHECK (samples > 0)
);
CREATE INDEX load_samples_target_ts ON load_samples (target_id, ts);
