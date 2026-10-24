-- Ungranted lock requests, used to describe what each waiter is waiting for.
--
-- This describes the request only. It deliberately does not join against
-- granted locks to guess which holder conflicts: lock conflicts depend on the
-- mode compatibility matrix and on wait-queue order, which pg_blocking_pids()
-- already evaluates correctly.
--
-- waitstart (PostgreSQL 14+) is when the wait began. It can be NULL for a very
-- short time after the wait starts.
--
-- Relation names are resolved only for the current database and for shared
-- catalogs (database = 0); OIDs from other databases would resolve to the
-- wrong object or not at all.
--
-- $1: PIDs of LockLens' own connections, excluded from the result.
SELECT l.pid,
       COALESCE(a.leader_pid, l.pid)  AS session_pid,
       l.locktype,
       l.mode,
       d.datname                      AS database,
       l.relation,
       CASE
           WHEN l.relation IS NOT NULL
            AND l.database IN (0, (SELECT oid FROM pg_catalog.pg_database
                                   WHERE datname = current_database()))
           THEN l.relation::regclass::text
       END                            AS relation_name,
       l.page,
       l.tuple,
       l.virtualxid,
       l.transactionid::text          AS transactionid,
       l.classid,
       l.objid,
       l.objsubid,
       l.waitstart
FROM pg_catalog.pg_locks AS l
LEFT JOIN pg_catalog.pg_stat_activity AS a ON a.pid = l.pid
LEFT JOIN pg_catalog.pg_database AS d ON d.oid = l.database
WHERE NOT l.granted
  AND l.pid IS NOT NULL
  AND l.pid <> ALL ($1::int[])
ORDER BY session_pid, l.pid, l.locktype, l.mode
