-- pg_stat_statements must be installed in the database LockLens connects to;
-- the view then covers statements of every database of the instance.
SELECT n.nspname, e.extversion
FROM pg_catalog.pg_extension AS e
JOIN pg_catalog.pg_namespace AS n ON n.oid = e.extnamespace
WHERE e.extname = 'pg_stat_statements'
