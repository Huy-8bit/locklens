SELECT id, name, enabled, kind, environments, target_ids, rule_ids, starts_at, ends_at, weekdays, start_time,
       duration_minutes, timezone, created_by, created_at, updated_at
FROM maintenance_windows ORDER BY name, id
