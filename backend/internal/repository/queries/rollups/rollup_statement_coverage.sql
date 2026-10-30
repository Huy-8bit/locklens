INSERT INTO statement_coverage (target_id, ts, covered_seconds, reset_detected)
SELECT target_id, date_bin('5 minutes', ts - interval '1 microsecond', 'epoch'::timestamptz),
       least(sum(interval_seconds), 300), bool_or(reset_detected)
FROM statement_collections
WHERE target_id = $1 AND ts > $2 AND ts <= $3 AND interval_seconds IS NOT NULL
GROUP BY 1, 2
ON CONFLICT (target_id, ts) DO UPDATE SET covered_seconds = EXCLUDED.covered_seconds, reset_detected = EXCLUDED.reset_detected
