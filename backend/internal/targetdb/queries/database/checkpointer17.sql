-- From PostgreSQL 17, checkpoints moved to pg_stat_checkpointer and backend
-- writes to pg_stat_io.
SELECT num_timed, num_requested, buffers_written, stats_reset
FROM pg_catalog.pg_stat_checkpointer
