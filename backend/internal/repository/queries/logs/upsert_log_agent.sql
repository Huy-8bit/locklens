INSERT INTO log_agents (token_id, target_id, hostname, version, sources, last_seen_at, last_event_at, received)
VALUES ($1, $2, $3, $4, $5, now(), $6, $7)
ON CONFLICT (token_id) DO UPDATE SET
    target_id = EXCLUDED.target_id, hostname = EXCLUDED.hostname, version = EXCLUDED.version, sources = EXCLUDED.sources,
    last_seen_at = now(),
    last_event_at = greatest(log_agents.last_event_at, EXCLUDED.last_event_at),
    received = log_agents.received + EXCLUDED.received
