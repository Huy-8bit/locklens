SELECT relid, datname, schemaname, relname, n_live_tup, n_dead_tup, n_mod_since_analyze, last_vacuum, last_autovacuum,
       last_analyze, last_autoanalyze, vacuum_count, autovacuum_count, analyze_count, autoanalyze_count,
       table_bytes, indexes_bytes, total_bytes, frozenxid_age
FROM table_stats WHERE target_id = $1 AND ts = $2 ORDER BY total_bytes DESC NULLS LAST
