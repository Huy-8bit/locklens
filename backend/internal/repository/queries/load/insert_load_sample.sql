INSERT INTO load_samples (target_id, ts, wait_class, wait_event, backend_type, dbid, datname, userid, usename, application_name, queryid, samples)
SELECT $1, $2, c, e, b, d::oid, dn, u::oid, un, a, q, n
FROM unnest($3::text[], $4::text[], $5::text[], $6::bigint[], $7::text[], $8::bigint[], $9::text[], $10::text[], $11::bigint[], $12::int[])
  AS x(c, e, b, d, dn, u, un, a, q, n)
