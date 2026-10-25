-- Roles for the LockLens lab primary. Local, fake credentials only.

-- demo_app performs the writes that create lock contention. LockLens never
-- uses it.
CREATE ROLE demo_app LOGIN PASSWORD 'demo-app-pw';
GRANT CREATE ON SCHEMA public TO demo_app;

-- locklens_observer is the recommended LockLens monitoring role: a login
-- role with the privileges of pg_read_all_stats. No superuser.
CREATE ROLE locklens_observer LOGIN PASSWORD 'observer-pw';
GRANT pg_read_all_stats TO locklens_observer;

-- locklens_limited is a plain login role, for exploring degraded visibility.
CREATE ROLE locklens_limited LOGIN PASSWORD 'limited-pw';

-- replicator streams WAL to the lab standby.
CREATE ROLE replicator LOGIN REPLICATION PASSWORD 'replicator-pw';

-- pg_stat_statements (preloaded via shared_preload_libraries) for the
-- workload views. Installed in the database LockLens connects to.
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

-- locklens_reader is the separate, least-privileged role for LockLens'
-- catalog and table-data browser: USAGE on the demo schemas and SELECT on
-- their tables, nothing else.
CREATE ROLE locklens_reader LOGIN PASSWORD 'reader-pw';
GRANT USAGE ON SCHEMA public TO locklens_reader;
ALTER DEFAULT PRIVILEGES FOR ROLE demo_app IN SCHEMA public GRANT SELECT ON TABLES TO locklens_reader;
ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public GRANT SELECT ON TABLES TO locklens_reader;
