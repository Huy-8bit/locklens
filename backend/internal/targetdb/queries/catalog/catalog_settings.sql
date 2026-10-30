-- Planner settings the optimizer uses, in their base units.
SELECT name, setting FROM pg_catalog.pg_settings
WHERE name IN ('work_mem', 'default_statistics_target', 'random_page_cost', 'effective_cache_size', 'server_version_num')
