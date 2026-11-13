INSERT INTO servers (id, hostname, version, started_at, heartbeat_at, roles) VALUES ($1, $2, $3, $4, now(), $5)
ON CONFLICT (id) DO UPDATE SET hostname = EXCLUDED.hostname, version = EXCLUDED.version, heartbeat_at = now(), roles = EXCLUDED.roles
