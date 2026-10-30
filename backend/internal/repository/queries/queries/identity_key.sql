SELECT server_major, dbid::bigint, userid::bigint, toplevel, queryid FROM query_identities WHERE target_id = $1 AND id = $2
