INSERT INTO query_rollups (target_id, identity_id, ts, calls, total_exec_ms, rows, shared_blks_read, shared_blks_hit, temp_blks_written)
SELECT target_id, identity_id, date_bin('5 minutes', ts - interval '1 microsecond', 'epoch'::timestamptz),
       sum(calls), sum(total_exec_ms), sum(rows), sum(shared_blks_read), sum(shared_blks_hit), sum(temp_blks_written)
FROM query_samples
WHERE target_id = $1 AND ts > $2 AND ts <= $3
GROUP BY 1, 2, 3
ON CONFLICT (target_id, identity_id, ts) DO UPDATE SET
    calls = EXCLUDED.calls, total_exec_ms = EXCLUDED.total_exec_ms, rows = EXCLUDED.rows,
    shared_blks_read = EXCLUDED.shared_blks_read, shared_blks_hit = EXCLUDED.shared_blks_hit,
    temp_blks_written = EXCLUDED.temp_blks_written
