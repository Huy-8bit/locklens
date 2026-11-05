INSERT INTO maintenance_windows (name, enabled, kind, environments, target_ids, rule_ids, starts_at, ends_at, weekdays,
    start_time, duration_minutes, timezone, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING id, name, enabled, kind, environments, target_ids, rule_ids, starts_at, ends_at, weekdays, start_time,
    duration_minutes, timezone, created_by, created_at, updated_at
