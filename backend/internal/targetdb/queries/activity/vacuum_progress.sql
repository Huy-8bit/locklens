-- Vacuum progress. Relation names resolve only in the current database.
SELECT p.pid, p.datname,
       CASE WHEN p.datname = current_database() THEN p.relid::regclass::text ELSE p.relid::text END,
       p.phase, p.heap_blks_total, p.heap_blks_scanned, p.heap_blks_vacuumed, p.index_vacuum_count
FROM pg_catalog.pg_stat_progress_vacuum AS p
ORDER BY p.pid
