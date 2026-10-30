SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY total_exec_ms / calls), count(*)
FROM query_rollups WHERE target_id = $1 AND identity_id = $2 AND ts >= $3 AND ts < $4 AND calls >= $5
