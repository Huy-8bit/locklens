UPDATE incidents SET acknowledged_at = now(), acknowledged_by = $2, ack_note = nullif($3, '')
WHERE id = $1 RETURNING id, target_id, target_name, rule_id, rule_version, severity, title, state, opened_at,
last_seen_at, resolved_at, resolution, trigger_config, acknowledged_at, acknowledged_by, ack_note, event_count,
size_bytes, truncated, source
