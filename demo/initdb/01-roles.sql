-- Roles for the LockLens demo and integration tests. Local, fake credentials.

-- demo_app performs the writes that create lock contention. It is never used
-- by the locklens CLI.
CREATE ROLE demo_app LOGIN PASSWORD 'demo-app-pw';
GRANT CREATE ON SCHEMA public TO demo_app;

-- locklens_observer is the recommended LockLens role: it can log in and has
-- the privileges of pg_read_all_stats, so it sees every session's details.
CREATE ROLE locklens_observer LOGIN PASSWORD 'observer-pw';
GRANT pg_read_all_stats TO locklens_observer;

-- locklens_limited is a plain login role, used to test degraded visibility.
CREATE ROLE locklens_limited LOGIN PASSWORD 'limited-pw';
