SELECT c.oid, n.nspname, c.relname, c.reltuples::float8, c.relpages::bigint,
       coalesce(s.seq_scan, 0), coalesce(s.seq_tup_read, 0), coalesce(s.idx_scan, 0),
       coalesce(s.n_tup_ins + s.n_tup_upd + s.n_tup_del, 0), coalesce(s.n_live_tup, 0),
       coalesce(s.n_mod_since_analyze, 0), s.last_analyze, s.last_autoanalyze, c.relispartition
FROM pg_catalog.pg_class AS c
JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
LEFT JOIN pg_catalog.pg_stat_user_tables AS s ON s.relid = c.oid
WHERE c.relkind IN ('r', 'p', 'm') AND n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_toast%'
ORDER BY c.relpages DESC
LIMIT $1
