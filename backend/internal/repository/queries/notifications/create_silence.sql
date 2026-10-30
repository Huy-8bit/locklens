INSERT INTO notification_silences (target_id, rule_id, starts_at, ends_at, reason, created_by) VALUES ($1, $2, now(), $3, $4, $5)
RETURNING id
