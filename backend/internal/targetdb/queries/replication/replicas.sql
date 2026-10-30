-- Replicas of a primary. LSN columns are NULL for roles without
-- pg_read_all_stats. *_lag columns are measured by the primary from standby
-- feedback and become NULL when the standby has caught up and is idle
-- (PostgreSQL documents this), so byte lags are reported alongside.
SELECT r.pid, r.usename, r.application_name, host(r.client_addr), r.state, r.sync_state,
       r.sent_lsn::text, r.write_lsn::text, r.flush_lsn::text, r.replay_lsn::text,
       pg_catalog.pg_wal_lsn_diff(pg_catalog.pg_current_wal_lsn(), r.sent_lsn)::float8,
       pg_catalog.pg_wal_lsn_diff(pg_catalog.pg_current_wal_lsn(), r.write_lsn)::float8,
       pg_catalog.pg_wal_lsn_diff(pg_catalog.pg_current_wal_lsn(), r.flush_lsn)::float8,
       pg_catalog.pg_wal_lsn_diff(pg_catalog.pg_current_wal_lsn(), r.replay_lsn)::float8,
       extract(epoch FROM r.write_lag)::float8, extract(epoch FROM r.flush_lag)::float8,
       extract(epoch FROM r.replay_lag)::float8, r.reply_time
FROM pg_catalog.pg_stat_replication AS r
ORDER BY r.application_name, r.pid
