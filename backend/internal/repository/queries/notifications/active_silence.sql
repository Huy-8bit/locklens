SELECT s.id, s.target_id, t.name, s.rule_id, s.starts_at, s.ends_at, s.reason, s.created_by, s.created_at
FROM notification_silences s LEFT JOIN targets t ON t.id = s.target_id
WHERE s.starts_at <= now() AND s.ends_at > now()
  AND (s.target_id IS NULL OR s.target_id = $1) AND (s.rule_id IS NULL OR s.rule_id = $2)
ORDER BY s.ends_at DESC LIMIT 1
