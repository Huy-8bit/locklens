SELECT count(*) FROM query_plans p JOIN query_identities q ON q.id = p.identity_id
WHERE p.target_id = $1 AND q.queryid = $2
