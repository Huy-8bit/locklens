-- A monitored instance of the scaling benchmark: the read-only observer
-- role LockLens connects with (fake, local-only password).
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
CREATE ROLE locklens_observer LOGIN PASSWORD 'bench-observer-pw';
GRANT pg_read_all_stats TO locklens_observer;
