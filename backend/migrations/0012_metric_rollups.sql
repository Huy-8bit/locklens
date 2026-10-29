-- Downsampled metric history. Raw metric_points are kept for the history
-- retention; one-minute and one-hour rollups are kept longer, so long ranges
-- read few rows and stay available after raw points expire. Each bucket keeps
-- what every aggregation needs (sum, max, last, time-weighted sums) and how
-- many of its samples were known, so an unknown sample keeps the bucket
-- unknown exactly as when it is computed from raw points.
CREATE TABLE metric_rollups_1m (
    target_id     bigint           NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    metric        text             NOT NULL,
    label         text             NOT NULL DEFAULT '',
    ts            timestamptz      NOT NULL,
    samples       bigint           NOT NULL,
    known         bigint           NOT NULL,
    vsum          double precision,
    vmax          double precision,
    vlast         double precision,
    last_ts       timestamptz      NOT NULL,
    wsum          double precision,
    wint          double precision,
    with_interval bigint           NOT NULL,
    PRIMARY KEY (target_id, metric, label, ts)
);

CREATE TABLE metric_rollups_1h (LIKE metric_rollups_1m INCLUDING ALL);
ALTER TABLE metric_rollups_1h ADD FOREIGN KEY (target_id) REFERENCES targets (id) ON DELETE CASCADE;
