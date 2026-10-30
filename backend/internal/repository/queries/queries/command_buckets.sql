SELECT date_bin($5::interval, q.ts, $2), i.command, sum(q.calls)::bigint, sum(q.total_exec_ms), sum(q.rows)::bigint
FROM query_samples q JOIN query_identities i ON i.id = q.identity_id
WHERE q.target_id = $1 AND q.ts > $2 AND q.ts <= $3 AND ($4 = '' OR i.username <> $4)
GROUP BY 1, 2 ORDER BY 1, 2
