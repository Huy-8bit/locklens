-- Blocker -> waiter relations, one row per waiting backend.
--
-- Waiters are found from ungranted pg_locks rows instead of from
-- pg_stat_activity.wait_event_type: pg_locks is readable by every role, while
-- wait_event_type is NULL for sessions the observer is not allowed to see.
--
-- pg_blocking_pids() is called once per waiting backend only. Each call needs
-- brief exclusive access to the lock manager, so calling it for every backend
-- would add needless load (and a per-session query loop would be N+1).
--
-- Parallel workers are folded into their group leader. pg_blocking_pids()
-- already reports blockers as client-visible (leader) PIDs and may repeat a
-- PID when several workers of one blocker are involved; duplicates are
-- removed by the caller.
--
-- A blocker PID of 0 means the conflicting lock is held by a prepared
-- transaction.
--
-- $1: PIDs of LockLens' own connections, excluded from the result.
WITH waiters AS (
    SELECT DISTINCT COALESCE(a.leader_pid, l.pid) AS pid
    FROM pg_catalog.pg_locks AS l
    LEFT JOIN pg_catalog.pg_stat_activity AS a ON a.pid = l.pid
    WHERE NOT l.granted
      AND l.pid IS NOT NULL
)
SELECT w.pid,
       pg_catalog.pg_blocking_pids(w.pid) AS blocking_pids
FROM waiters AS w
WHERE w.pid <> ALL ($1::int[])
ORDER BY w.pid
