-- Server facts and observer privileges, read once per collection.
--
-- pg_has_role(..., 'USAGE') is true for superusers and for roles that inherit
-- the privileges of pg_read_all_stats (for example through pg_monitor). Only
-- such roles can see every column of pg_stat_activity for other roles'
-- sessions.
SELECT
    current_setting('server_version_num')::int        AS version_num,
    current_setting('server_version')                 AS version,
    current_database()                                AS database,
    pg_catalog.pg_has_role('pg_read_all_stats', 'USAGE') AS read_all_stats,
    current_setting('track_activities')::bool         AS track_activities
