SELECT t.relid, current_database(), t.schemaname, t.relname, t.n_live_tup, t.n_dead_tup,
       t.n_mod_since_analyze, t.last_vacuum, t.last_autovacuum, t.last_analyze, t.last_autoanalyze,
       t.vacuum_count, t.autovacuum_count, t.analyze_count, t.autoanalyze_count,
       c.relpages::bigint * current_setting('block_size')::bigint, NULL::bigint, NULL::bigint,
       pg_catalog.age(c.relfrozenxid)::bigint
FROM pg_catalog.pg_stat_user_tables AS t
JOIN pg_catalog.pg_class AS c ON c.oid = t.relid
ORDER BY c.relpages DESC
LIMIT 200
