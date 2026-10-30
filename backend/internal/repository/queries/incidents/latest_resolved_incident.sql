SELECT id, target_id, target_name, rule_id, rule_version, severity, title, state, opened_at,
last_seen_at, resolved_at, resolution, trigger_config, acknowledged_at, acknowledged_by, ack_note, event_count,
size_bytes, truncated, source FROM incidents
WHERE target_id = $1 AND rule_id = $2 AND state = 'resolved' AND resolved_at >= $3 AND source = 'recorder'
ORDER BY resolved_at DESC LIMIT 1
