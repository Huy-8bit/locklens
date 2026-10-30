-- Session metadata from pg_stat_activity.
--
-- Returned rows: every backend involved in blocking ($2) plus every client
-- backend with an open transaction, so long-running and idle-in-transaction
-- sessions can be reported. Parallel workers are not returned as sessions;
-- they are counted on their leader.
--
-- For sessions of other roles, an observer without the privileges of
-- pg_read_all_stats sees only identifying columns (pid, datname, usename,
-- application_name, transaction IDs); state, wait events, timestamps,
-- client_addr, and backend_type are NULL and query is the literal
-- '<insufficient privilege>', which the caller discards. details_visible
-- reports that case. Because xact_start is NULL for such sessions, their open
-- transactions only appear here when they take part in blocking.
--
-- Query text is only read when $3 is true and is cut to $4 characters on the
-- server so large statements are never transferred.
--
-- $1: PIDs of LockLens' own connections (excluded)
-- $2: PIDs involved in blocking (waiters and blockers)
-- $3: include query text
-- $4: maximum query length in characters
WITH workers AS (
    SELECT leader_pid, count(*) AS n
    FROM pg_catalog.pg_stat_activity
    WHERE backend_type = 'parallel worker'
      AND leader_pid IS NOT NULL
    GROUP BY leader_pid
)
SELECT a.pid,
       a.leader_pid,
       a.datname,
       a.usename,
       a.application_name,
       host(a.client_addr)                      AS client_addr,
       a.backend_type,
       a.state,
       a.wait_event_type,
       a.wait_event,
       a.backend_start,
       a.xact_start,
       a.query_start,
       a.state_change,
       a.backend_xid::text                      AS backend_xid,
       CASE WHEN $3::bool THEN left(a.query, $4::int) END            AS query,
       CASE WHEN $3::bool THEN char_length(a.query) > $4::int END    AS query_truncated,
       CASE
           WHEN pg_catalog.pg_has_role('pg_read_all_stats', 'USAGE') THEN true
           WHEN a.usesysid IS NULL THEN false
           -- pg_has_role() raises an error for an OID that is not a role, so
           -- guard against a role dropped while its session is still alive.
           WHEN NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles r WHERE r.oid = a.usesysid) THEN false
           ELSE pg_catalog.pg_has_role(a.usesysid, 'USAGE')
       END                                      AS details_visible,
       COALESCE(w.n, 0)::int                    AS parallel_workers,
       -- The top-level statement's pg_stat_statements queryid (PostgreSQL
       -- 14+, computed with compute_query_id); NULL when not computed.
       a.query_id
FROM pg_catalog.pg_stat_activity AS a
LEFT JOIN workers AS w ON w.leader_pid = a.pid
WHERE a.pid <> ALL ($1::int[])
  AND (a.pid = ANY ($2::int[])
       OR (a.xact_start IS NOT NULL AND a.backend_type = 'client backend'))
ORDER BY a.pid
