SELECT current_setting('server_version'),
       current_setting('server_version_num')::int,
       pg_catalog.pg_is_in_recovery(),
       pg_catalog.pg_has_role('pg_read_all_stats', 'USAGE'),
       current_setting('track_activities')::bool,
       current_setting('track_io_timing')::bool
