SELECT q.server_major, q.dbid::bigint, q.userid::bigint, q.toplevel, q.queryid, q.id, q.datname, q.username, q.command,
       CASE WHEN $7 THEN q.query_text END
FROM query_identities q
JOIN unnest($2::int[], $3::bigint[], $4::bigint[], $5::bool[], $6::bigint[]) AS k(m, d, u, t, qid)
  ON q.server_major = k.m AND q.dbid = k.d::oid AND q.userid = k.u::oid AND q.toplevel = k.t AND q.queryid = k.qid
WHERE q.target_id = $1
