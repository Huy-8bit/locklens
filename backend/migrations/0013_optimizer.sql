-- The query optimizer: the latest schema catalog of each target and the
-- recommendations derived from it and from the workload, with their life
-- cycle (decisions, application, verification before and after).

-- The latest catalog (indexes, tables, foreign keys, column statistics
-- without data values) of the database each target connects to.
CREATE TABLE catalog_state (
    target_id    bigint      PRIMARY KEY REFERENCES targets (id) ON DELETE CASCADE,
    collected_at timestamptz NOT NULL,
    catalog      jsonb       NOT NULL
);

CREATE TABLE recommendations (
    id            bigserial        PRIMARY KEY,
    target_id     bigint           NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    kind          text             NOT NULL,
    rec_key       text             NOT NULL,
    -- open, accepted, dismissed, applied, verified, no_effect, regressed, resolved
    status        text             NOT NULL DEFAULT 'open',
    severity      text             NOT NULL,
    title         text             NOT NULL,
    summary       text             NOT NULL,
    details       jsonb            NOT NULL,
    ddl           text,
    benefit       double precision NOT NULL DEFAULT 0,
    confidence    jsonb            NOT NULL,
    first_seen    timestamptz      NOT NULL,
    last_seen     timestamptz      NOT NULL,
    decided_by    text,
    decided_at    timestamptz,
    note          text             NOT NULL DEFAULT '',
    applied_at    timestamptz,
    applied_how   text,
    -- Statement time per call before and after the change (verification).
    before_ms     double precision,
    after_ms      double precision,
    verified_at   timestamptz,
    -- The result of a what-if check with hypothetical indexes (HypoPG).
    whatif        jsonb,
    resolved_at   timestamptz,
    UNIQUE (target_id, kind, rec_key)
);
CREATE INDEX recommendations_target_status ON recommendations (target_id, status);

-- What-if checks with HypoPG are opt-in per target (the extension must be
-- installed on the target by its owner).
ALTER TABLE workspace_policies ADD COLUMN hypopg_enabled boolean NOT NULL DEFAULT false;
