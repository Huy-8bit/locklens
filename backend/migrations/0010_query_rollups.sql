-- Five-minute rollups of statement activity, the source of query baselines.
-- They are maintained incrementally from query_samples (the last buckets are
-- recomputed on every pass, so late writes are included) and kept longer than
-- raw samples, so a baseline can span weeks without scanning raw history.
CREATE TABLE query_rollups (
    target_id         bigint           NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    identity_id       bigint           NOT NULL REFERENCES query_identities (id) ON DELETE CASCADE,
    ts                timestamptz      NOT NULL,
    calls             bigint           NOT NULL,
    total_exec_ms     double precision NOT NULL,
    rows              bigint           NOT NULL,
    shared_blks_read  bigint           NOT NULL,
    shared_blks_hit   bigint           NOT NULL,
    temp_blks_written bigint           NOT NULL,
    PRIMARY KEY (target_id, identity_id, ts)
);
CREATE INDEX query_rollups_target_ts ON query_rollups (target_id, ts);

-- How much of each five-minute bucket the statements collector covered, and
-- whether pg_stat_statements was reset in it. A statement absent from a
-- covered bucket did not run (zero calls); an uncovered bucket is unknown.
CREATE TABLE statement_coverage (
    target_id       bigint           NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    ts              timestamptz      NOT NULL,
    covered_seconds double precision NOT NULL,
    reset_detected  boolean          NOT NULL DEFAULT false,
    PRIMARY KEY (target_id, ts)
);

-- Statement findings carry a graded confidence and the measurements behind
-- them (used for impact ranking).
ALTER TABLE findings ADD COLUMN confidence jsonb, ADD COLUMN metrics jsonb;
