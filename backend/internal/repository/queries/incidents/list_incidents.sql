SELECT id, target_id, target_name, rule_id, rule_version, severity, title, state, opened_at,
last_seen_at, resolved_at, resolution, trigger_config, acknowledged_at, acknowledged_by, ack_note, event_count,
size_bytes, truncated, source, count(*) OVER ()
FROM incidents
WHERE ($1 = 0 OR target_id = $1) AND ($2 = '' OR state = $2) AND ($3 = '' OR rule_id = $3)
  AND ($4::timestamptz IS NULL OR coalesce(resolved_at, 'infinity') >= $4)
  AND ($5::timestamptz IS NULL OR opened_at <= $5)
ORDER BY opened_at DESC, id DESC
LIMIT $6 OFFSET $7
