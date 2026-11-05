UPDATE maintenance_windows SET name = $2, enabled = $3, kind = $4, environments = $5, target_ids = $6, rule_ids = $7,
    starts_at = $8, ends_at = $9, weekdays = $10, start_time = $11, duration_minutes = $12, timezone = $13, updated_at = now()
WHERE id = $1
RETURNING id, name, enabled, kind, environments, target_ids, rule_ids, starts_at, ends_at, weekdays, start_time,
    duration_minutes, timezone, created_by, created_at, updated_at
