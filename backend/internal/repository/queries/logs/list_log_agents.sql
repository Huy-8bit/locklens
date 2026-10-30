SELECT a.token_id, t.name, a.hostname, a.version, a.sources, a.first_seen_at, a.last_seen_at, a.last_event_at, a.received
FROM log_agents a JOIN api_tokens t ON t.id = a.token_id
WHERE a.target_id = $1 AND t.revoked_at IS NULL
ORDER BY a.last_seen_at DESC
