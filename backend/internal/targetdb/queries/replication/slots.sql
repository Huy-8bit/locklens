-- Replication slots and the WAL each one retains (distance from restart_lsn
-- to the current or received position). safe_wal_size is NULL unless
-- max_slot_wal_keep_size is set.
SELECT s.slot_name, s.slot_type, s.active, s.wal_status, s.restart_lsn::text,
       pg_catalog.pg_wal_lsn_diff(
           CASE WHEN pg_catalog.pg_is_in_recovery() THEN pg_catalog.pg_last_wal_receive_lsn()
                ELSE pg_catalog.pg_current_wal_lsn() END, s.restart_lsn)::float8,
       s.safe_wal_size
FROM pg_catalog.pg_replication_slots AS s
ORDER BY s.slot_name
