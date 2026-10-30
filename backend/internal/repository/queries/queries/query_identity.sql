SELECT id, server_major, datname, username, toplevel, queryid, command, CASE WHEN $3 THEN query_text END, first_seen, last_seen
FROM query_identities WHERE target_id = $1 AND id = $2
