-- The databases the monitoring role may connect to (not templates), the
-- connected one first.
SELECT datname FROM pg_catalog.pg_database
WHERE datallowconn AND NOT datistemplate AND has_database_privilege(datname, 'CONNECT')
ORDER BY datname <> current_database(), datname
LIMIT $1
