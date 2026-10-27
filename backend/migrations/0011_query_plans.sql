-- Execution plans saved for a statement, to compare plans over time. Plans
-- are saved only on an explicit request (from the SQL workspace or the
-- statement page); LockLens never runs EXPLAIN ANALYZE, and EXPLAIN itself
-- runs only through the audited, read-only investigation workspace.
CREATE TABLE query_plans (
    id          bigserial        PRIMARY KEY,
    target_id   bigint           NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    identity_id bigint           NOT NULL REFERENCES query_identities (id) ON DELETE CASCADE,
    captured_at timestamptz      NOT NULL DEFAULT now(),
    captured_by text             NOT NULL,
    -- workspace_explain, generic_plan, or manual.
    source      text             NOT NULL,
    -- json (EXPLAIN (FORMAT JSON)) or text.
    format      text             NOT NULL,
    plan        text             NOT NULL,
    -- Hash of the plan shape (node types, relations, indexes), ignoring
    -- costs and row estimates: equal fingerprints mean the same plan shape.
    fingerprint text             NOT NULL,
    total_cost  double precision,
    note        text             NOT NULL DEFAULT '',
    CHECK (format IN ('json', 'text')),
    CHECK (source IN ('workspace_explain', 'generic_plan', 'manual'))
);
CREATE INDEX query_plans_identity ON query_plans (target_id, identity_id, captured_at DESC);
