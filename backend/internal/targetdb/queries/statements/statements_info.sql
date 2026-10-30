-- Evictions and the last reset of pg_stat_statements.
-- {{view}}: the schema-qualified pg_stat_statements_info view.
SELECT dealloc, stats_reset FROM {{view}}
