-- Every setting with its value, unit, and where it was set. Settings a role
-- may not read show as such (pg_read_all_settings grants them).
SELECT name, coalesce(setting, ''), coalesce(unit, ''), source, pending_restart
FROM pg_catalog.pg_settings
ORDER BY name
