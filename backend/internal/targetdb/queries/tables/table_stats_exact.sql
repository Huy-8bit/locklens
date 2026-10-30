-- Table statistics of the database LockLens connects to (pg_stat_user_tables
-- is per database). Limited to the 200 largest tables. Size functions take an
-- ACCESS SHARE lock per relation; the collector runs them under a short
-- lock_timeout and falls back to relpages-based estimates if a relation is
-- locked exclusively.
SELECT t.relid, current_database(), t.schemaname, t.relname, t.n_live_tup, t.n_dead_tup,
       t.n_mod_since_analyze, t.last_vacuum, t.last_autovacuum, t.last_analyze, t.last_autoanalyze,
       t.vacuum_count, t.autovacuum_count, t.analyze_count, t.autoanalyze_count,
       pg_catalog.pg_table_size(t.relid), pg_catalog.pg_indexes_size(t.relid),
       pg_catalog.pg_total_relation_size(t.relid), pg_catalog.age(c.relfrozenxid)::bigint
FROM pg_catalog.pg_stat_user_tables AS t
JOIN pg_catalog.pg_class AS c ON c.oid = t.relid
ORDER BY pg_catalog.pg_total_relation_size(t.relid) DESC NULLS LAST
LIMIT 200
