-- At most 200 rows are shown (201 are read to tell that there are more).
SELECT p.proname AS name, pg_get_function_identity_arguments(p.oid) AS arguments,
CASE p.prokind WHEN 'f' THEN 'function' WHEN 'p' THEN 'procedure' WHEN 'a' THEN 'aggregate' WHEN 'w' THEN 'window' END AS kind,
pg_get_function_result(p.oid) AS returns, l.lanname AS language
FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace JOIN pg_language l ON l.oid = p.prolang
WHERE n.nspname = $1 AND has_schema_privilege(n.oid, 'USAGE') ORDER BY p.proname LIMIT 201
