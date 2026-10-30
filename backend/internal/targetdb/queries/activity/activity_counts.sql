-- Counts of client backends by state. UsedConnections includes LockLens'
-- own sessions because they consume max_connections slots too; every other
-- count excludes them ($1).
SELECT count(*) FILTER (WHERE backend_type = 'client backend'),
       count(*) FILTER (WHERE backend_type = 'client backend' AND pid <> ALL ($1::int[])),
       count(*) FILTER (WHERE backend_type = 'client backend' AND pid <> ALL ($1::int[]) AND state = 'active'),
       count(*) FILTER (WHERE backend_type = 'client backend' AND pid <> ALL ($1::int[]) AND state = 'idle'),
       count(*) FILTER (WHERE backend_type = 'client backend' AND pid <> ALL ($1::int[]) AND state = 'idle in transaction'),
       count(*) FILTER (WHERE backend_type = 'client backend' AND pid <> ALL ($1::int[]) AND state = 'idle in transaction (aborted)'),
       count(*) FILTER (WHERE pid <> ALL ($1::int[]) AND wait_event_type = 'Lock'),
       current_setting('max_connections')::int,
       current_setting('superuser_reserved_connections')::int
         + coalesce(nullif(current_setting('reserved_connections', true), '')::int, 0)
FROM pg_catalog.pg_stat_activity
