-- At most 200 rows are shown (201 are read to tell that there are more).
SELECT t.typname AS name,
CASE t.typtype WHEN 'c' THEN 'composite' WHEN 'd' THEN 'domain' WHEN 'e' THEN 'enum' WHEN 'r' THEN 'range' WHEN 'm' THEN 'multirange' END AS kind,
format_type(t.oid, NULL) AS definition
FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace LEFT JOIN pg_class c ON c.oid = t.typrelid
WHERE n.nspname = $1 AND has_schema_privilege(n.oid, 'USAGE') AND t.typtype IN ('c', 'd', 'e', 'r', 'm')
  AND (t.typrelid = 0 OR c.relkind = 'c') ORDER BY t.typname LIMIT 201
