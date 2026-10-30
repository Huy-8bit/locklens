SELECT identity_id, date_bin($4::interval, ts, 'epoch'::timestamptz), sum(calls)::bigint, sum(total_exec_ms), sum(rows)::bigint,
       sum(shared_blks_read)::bigint, sum(shared_blks_hit)::bigint, sum(temp_blks_written)::bigint
FROM query_rollups
WHERE target_id = $1 AND ts >= $2 AND ts < $3 AND identity_id = ANY ($5::bigint[])
GROUP BY 1, 2 ORDER BY 1, 2
