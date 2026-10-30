-- What the setup connection may do: its role, the server version, whether it
-- is a standby, a superuser, sees all sessions, is administrative (or can use
-- server files), and whether pg_stat_statements is installed.
SELECT current_user, current_setting('server_version'), current_setting('server_version_num')::int, pg_is_in_recovery(), r.rolsuper,
       pg_has_role(current_user, 'pg_read_all_stats', 'MEMBER'),
       r.rolsuper OR r.rolcreaterole OR r.rolcreatedb OR r.rolbypassrls OR EXISTS (
           SELECT 1 FROM pg_roles x WHERE pg_has_role(current_user, x.oid, 'MEMBER') AND (x.rolsuper OR x.rolcreaterole OR x.rolcreatedb
           OR x.rolbypassrls OR x.rolname IN ('pg_write_all_data', 'pg_read_server_files', 'pg_write_server_files', 'pg_execute_server_program'))),
       EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements')
FROM pg_roles r WHERE r.rolname = current_user
