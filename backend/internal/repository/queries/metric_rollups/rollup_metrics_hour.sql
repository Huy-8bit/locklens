INSERT INTO metric_rollups_1h (target_id, metric, label, ts, samples, known, vsum, vmax, vlast, last_ts, wsum, wint, with_interval)
SELECT target_id, metric, label, date_bin('1 hour', ts, 'epoch'::timestamptz),
       sum(samples), sum(known), sum(vsum), max(vmax), (array_agg(vlast ORDER BY last_ts DESC))[1], max(last_ts),
       sum(wsum), sum(wint), sum(with_interval)
FROM metric_rollups_1m
WHERE target_id = $1 AND ts >= $2 AND ts < $3
GROUP BY target_id, metric, label, 4
ON CONFLICT (target_id, metric, label, ts) DO UPDATE SET
    samples = EXCLUDED.samples, known = EXCLUDED.known, vsum = EXCLUDED.vsum, vmax = EXCLUDED.vmax,
    vlast = EXCLUDED.vlast, last_ts = EXCLUDED.last_ts, wsum = EXCLUDED.wsum, wint = EXCLUDED.wint,
    with_interval = EXCLUDED.with_interval
