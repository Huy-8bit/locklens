-- Cumulative counters per database. The row with datid 0 (shared objects)
-- exists from PostgreSQL 15; it is included like any other scope.
SELECT coalesce(datid, 0)::bigint, xact_commit, xact_rollback, blks_read, blks_hit,
       temp_files, temp_bytes, deadlocks, tup_returned, tup_fetched, tup_inserted, tup_updated, tup_deleted, stats_reset
FROM pg_catalog.pg_stat_database
