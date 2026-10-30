-- pg_stat_io (PostgreSQL 16+), summed over backend types, objects, and contexts.
SELECT coalesce(sum(reads), 0)::float8, coalesce(sum(writes), 0)::float8,
       coalesce(sum(extends), 0)::float8, max(stats_reset)
FROM pg_catalog.pg_stat_io
