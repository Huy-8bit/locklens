INSERT INTO query_identities (target_id, server_major, dbid, userid, toplevel, queryid, datname, username,
    query_text, first_seen, last_seen, command)
SELECT $1, m, d::oid, u::oid, t, q, dn, un, qt, $10, $10, c
FROM unnest($2::int[], $3::bigint[], $4::bigint[], $5::bool[], $6::bigint[], $7::text[], $8::text[], $9::text[], $12::text[])
     AS x(m, d, u, t, q, dn, un, qt, c)
ON CONFLICT (target_id, server_major, dbid, userid, toplevel, queryid) DO UPDATE
SET last_seen = EXCLUDED.last_seen, datname = EXCLUDED.datname, username = EXCLUDED.username,
    command = CASE WHEN EXCLUDED.command = 'other' THEN query_identities.command ELSE EXCLUDED.command END,
    query_text = CASE WHEN $11::bool THEN coalesce(EXCLUDED.query_text, query_identities.query_text) ELSE NULL END
RETURNING id, server_major, dbid, userid, toplevel, queryid
