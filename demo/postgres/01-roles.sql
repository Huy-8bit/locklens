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
