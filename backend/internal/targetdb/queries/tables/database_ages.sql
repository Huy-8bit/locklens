-- Database sizes need CONNECT on the database or pg_read_all_stats; others
-- are reported as unknown instead of failing the whole query.
SELECT d.datname, pg_catalog.age(d.datfrozenxid)::bigint, pg_catalog.mxid_age(d.datminmxid)::bigint,
       CASE WHEN pg_catalog.has_database_privilege(d.datname, 'CONNECT')
              OR pg_catalog.pg_has_role('pg_read_all_stats', 'USAGE')
            THEN pg_catalog.pg_database_size(d.datname) END
FROM pg_catalog.pg_database AS d
WHERE NOT d.datistemplate
ORDER BY d.datname
