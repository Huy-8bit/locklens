-- pg_stats shows only columns the role may read; most_common_vals and
-- histogram_bounds (data values) are never selected.
SELECT schemaname, tablename, attname, null_frac::float8, n_distinct::float8, correlation::float8
FROM pg_catalog.pg_stats
WHERE schemaname NOT IN ('pg_catalog', 'information_schema') AND schemaname NOT LIKE 'pg\_toast%'
LIMIT $1
