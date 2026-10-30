SELECT r.identity_id, q.server_major, q.dbid::bigint, q.userid::bigint, q.toplevel, q.queryid, sum(r.total_exec_ms)
FROM query_rollups r JOIN query_identities q ON q.id = r.identity_id
WHERE r.target_id = $1 AND r.ts >= $2
GROUP BY 1, 2, 3, 4, 5, 6
ORDER BY 7 DESC
LIMIT $3
