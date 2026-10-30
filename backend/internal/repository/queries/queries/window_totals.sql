SELECT coalesce(sum(calls), 0), coalesce(sum(total_exec_ms), 0), coalesce(sum(rows), 0)
FROM query_samples WHERE target_id = $1 AND identity_id = $2 AND ts > $3 AND ts <= $4
