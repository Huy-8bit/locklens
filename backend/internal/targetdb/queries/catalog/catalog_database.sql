-- The connected database, when its statistics were reset, and whether the
-- server is a standby.
SELECT current_database(),
       (SELECT stats_reset FROM pg_catalog.pg_stat_database WHERE datname = current_database()),
       pg_catalog.pg_is_in_recovery()
