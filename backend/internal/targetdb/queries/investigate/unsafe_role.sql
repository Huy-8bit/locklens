-- Whether the current role is administrative or can read or write server files
-- (directly or through a role it belongs to); the workspace refuses such roles.
SELECT r.rolsuper OR r.rolcreaterole OR r.rolcreatedb OR r.rolbypassrls OR EXISTS(SELECT 1 FROM pg_roles x WHERE pg_has_role(current_user,x.oid,'MEMBER') AND (x.rolsuper OR x.rolcreaterole OR x.rolcreatedb OR x.rolbypassrls OR x.rolname IN ('pg_write_all_data','pg_read_server_files','pg_write_server_files','pg_execute_server_program'))) FROM pg_roles r WHERE r.rolname=current_user
