-- Up to PostgreSQL 16, checkpoint and background writer counters share one view.
SELECT checkpoints_timed, checkpoints_req, buffers_checkpoint, buffers_clean,
       maxwritten_clean, buffers_backend, buffers_alloc, stats_reset
FROM pg_catalog.pg_stat_bgwriter
