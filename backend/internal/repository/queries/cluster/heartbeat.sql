INSERT INTO servers (id, hostname, version, started_at, heartbeat_at, roles, capacity, load, max_targets)
VALUES ($1, $2, $3, $4, now(), $5, $6, $7, $8)
ON CONFLICT (id) DO UPDATE SET hostname = EXCLUDED.hostname, version = EXCLUDED.version, heartbeat_at = now(),
    roles = EXCLUDED.roles, capacity = EXCLUDED.capacity, load = EXCLUDED.load, max_targets = EXCLUDED.max_targets
