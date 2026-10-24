-- Prepared (two-phase) transactions. pg_blocking_pids() reports a blocking
-- prepared transaction as PID 0 without saying which one, so all of them are
-- listed as context.
SELECT gid,
       transaction::text AS transaction,
       prepared,
       owner,
       database
FROM pg_catalog.pg_prepared_xacts
ORDER BY prepared, gid
