SELECT q.server_major, q.dbid::bigint, q.userid::bigint, q.toplevel, q.queryid, q.id, q.datname, q.username, q.command,
       CASE WHEN $5 THEN q.query_text END
FROM unnest($2::bigint[], $3::bigint[], $4::bigint[]) AS k(d, u, qid)
CROSS JOIN LATERAL (
    SELECT * FROM query_identities i
    WHERE i.target_id = $1 AND i.dbid = k.d::oid AND i.userid = k.u::oid AND i.queryid = k.qid AND i.toplevel
    ORDER BY i.last_seen DESC LIMIT 1
) AS q
