-- Client connections grouped by database, user, application, client address,
-- and state; LockLens' own sessions ($1) are excluded. Bounded to 200 groups.
SELECT coalesce(datname, ''), coalesce(usename, ''), coalesce(application_name, ''),
       coalesce(host(client_addr), ''), coalesce(state, 'unknown'), count(*)::int,
       extract(epoch FROM max(now() - backend_start))::float8
FROM pg_catalog.pg_stat_activity
WHERE backend_type = 'client backend' AND pid <> ALL ($1::int[])
GROUP BY 1, 2, 3, 4, 5
ORDER BY 6 DESC, 1, 2, 3, 4, 5
LIMIT 200
