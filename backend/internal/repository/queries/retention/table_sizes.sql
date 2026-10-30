SELECT c.relname, pg_total_relation_size(c.oid), greatest(c.reltuples, 0)::bigint, NULL::bigint, NULL::bigint
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema() AND c.relkind = 'r'
ORDER BY 2 DESC
