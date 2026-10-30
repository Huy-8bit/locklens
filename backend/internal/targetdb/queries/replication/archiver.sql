SELECT current_setting('archive_mode'), archived_count, failed_count, last_archived_time,
       last_failed_time, last_failed_wal
FROM pg_catalog.pg_stat_archiver
