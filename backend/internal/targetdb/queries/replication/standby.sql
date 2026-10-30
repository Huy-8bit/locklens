-- Standby status. pg_last_xact_replay_timestamp() is the commit time of the
-- last replayed transaction: when the primary is idle it grows without any
-- real lag, so LockLens never presents it as replication lag.
SELECT pg_catalog.pg_last_wal_receive_lsn()::text, pg_catalog.pg_last_wal_replay_lsn()::text,
       pg_catalog.pg_wal_lsn_diff(pg_catalog.pg_last_wal_receive_lsn(), pg_catalog.pg_last_wal_replay_lsn())::float8,
       pg_catalog.pg_last_xact_replay_timestamp(), pg_catalog.pg_get_wal_replay_pause_state()
