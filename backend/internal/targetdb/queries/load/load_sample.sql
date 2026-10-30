-- The sessions that are load right now: client backends, parallel workers,
-- and autovacuum workers that are not idle, except LockLens' own ($1). Only
-- labels and wait information are read; the statement is identified by
-- query_id (PostgreSQL 14+), never by its text.
SELECT a.state, a.wait_event_type, coalesce(a.wait_event, ''), coalesce(a.backend_type, ''),
       coalesce(a.datid::bigint, 0), coalesce(a.datname, ''), coalesce(a.usesysid::bigint, 0), coalesce(a.usename, ''),
       left(coalesce(a.application_name, ''), 64), coalesce(a.query_id, 0)
FROM pg_catalog.pg_stat_activity AS a
WHERE a.pid <> ALL ($1::int[])
  AND a.backend_type IN ('client backend', 'parallel worker', 'autovacuum worker')
  AND a.state IS DISTINCT FROM 'idle'
