INSERT INTO metric_rollups_1m (target_id, metric, label, ts, samples, known, vsum, vmax, vlast, last_ts, wsum, wint, with_interval)
SELECT target_id, metric, label, date_bin('1 minute', ts - interval '1 microsecond', 'epoch'::timestamptz),
       count(*), count(value), sum(value), max(value), (array_agg(value ORDER BY ts DESC))[1], max(ts),
       sum(value * interval_seconds), sum(interval_seconds), count(interval_seconds)
FROM metric_points
WHERE target_id = $1 AND ts > $2 AND ts <= $3
GROUP BY target_id, metric, label, 4
ON CONFLICT (target_id, metric, label, ts) DO UPDATE SET
    samples = EXCLUDED.samples, known = EXCLUDED.known, vsum = EXCLUDED.vsum, vmax = EXCLUDED.vmax,
    vlast = EXCLUDED.vlast, last_ts = EXCLUDED.last_ts, wsum = EXCLUDED.wsum, wint = EXCLUDED.wint,
    with_interval = EXCLUDED.with_interval
