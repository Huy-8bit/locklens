SELECT s.id, s.target_id, t.name, s.rule_id, s.starts_at, s.ends_at, s.reason, s.created_by, s.created_at
FROM notification_silences s LEFT JOIN targets t ON t.id = s.target_id
WHERE $1 OR s.ends_at > now()
ORDER BY s.ends_at DESC LIMIT $2
