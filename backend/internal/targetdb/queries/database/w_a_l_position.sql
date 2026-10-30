-- WAL position. On a primary pg_current_wal_lsn() grows with WAL generation;
-- on a standby the received position is used instead.
SELECT CASE WHEN pg_catalog.pg_is_in_recovery()
            THEN pg_catalog.pg_wal_lsn_diff(pg_catalog.pg_last_wal_receive_lsn(), '0/0')
            ELSE pg_catalog.pg_wal_lsn_diff(pg_catalog.pg_current_wal_lsn(), '0/0') END::float8
