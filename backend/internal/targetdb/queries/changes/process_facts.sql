SELECT pg_catalog.pg_postmaster_start_time(), pg_catalog.pg_conf_load_time(), pg_catalog.pg_is_in_recovery(),
       current_setting('server_version_num')::int, current_setting('server_version')
