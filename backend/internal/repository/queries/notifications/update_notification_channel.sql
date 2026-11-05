UPDATE notification_channels SET name = $2, enabled = $3, min_severity = $4, environments = $5, on_open = $6, on_escalate = $7,
    on_resolve = $8, reminder_seconds = $9, config_ciphertext = coalesce($10, config_ciphertext),
    display = CASE WHEN $10::bytea IS NULL THEN display ELSE $11 END, rule_ids = $12, target_ids = $13, delay_seconds = $14,
    updated_at = now()
WHERE id = $1
RETURNING id, name, kind, enabled, min_severity, environments, on_open, on_escalate, on_resolve,
reminder_seconds, config_ciphertext, display, created_at, updated_at, rule_ids, target_ids, delay_seconds
