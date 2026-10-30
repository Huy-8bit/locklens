-- Servers with a recent heartbeat; servers silent for a day are forgotten.
WITH gone AS (DELETE FROM servers WHERE heartbeat_at < now() - interval '1 day')
SELECT id, hostname, version, started_at, heartbeat_at, heartbeat_at >= now() - make_interval(secs => $1) AS alive
FROM servers ORDER BY id
