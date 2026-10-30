-- Fails when pg_stat_statements is not preloaded; run under a savepoint.
-- {{view}}: the schema-qualified pg_stat_statements view.
SELECT 1 FROM {{view}} LIMIT 1
