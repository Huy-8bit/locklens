INSERT INTO table_stats (target_id, ts, relid, datname, schemaname, relname, n_live_tup, n_dead_tup,
    n_mod_since_analyze, last_vacuum, last_autovacuum, last_analyze, last_autoanalyze, vacuum_count,
    autovacuum_count, analyze_count, autoanalyze_count, table_bytes, indexes_bytes, total_bytes, frozenxid_age)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
ON CONFLICT DO NOTHING
