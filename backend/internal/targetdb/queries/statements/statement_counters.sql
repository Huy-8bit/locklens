-- The cumulative counters of pg_stat_statements without the statement texts
-- ({{function}}(false)): texts live in a file that the extension reads in full
-- whenever they are requested, the most expensive part of a collection.
-- Rows of other roles without pg_read_all_stats have a NULL queryid.
-- {{function}}: the schema-qualified pg_stat_statements function.
SELECT s.userid, s.dbid, s.toplevel, s.queryid,
       coalesce(d.datname, s.dbid::text), coalesce(r.rolname, s.userid::text),
       s.calls, s.total_exec_time, s.rows, s.shared_blks_hit, s.shared_blks_read,
       s.temp_blks_written, s.wal_bytes::float8
FROM {{function}}(false) AS s
LEFT JOIN pg_catalog.pg_database AS d ON d.oid = s.dbid
LEFT JOIN pg_catalog.pg_roles AS r ON r.oid = s.userid
