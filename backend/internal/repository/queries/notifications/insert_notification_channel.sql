INSERT INTO notification_channels (name, kind, enabled, min_severity, environments, on_open, on_escalate, on_resolve, reminder_seconds, config_ciphertext, display)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, name, kind, enabled, min_severity, environments, on_open, on_escalate, on_resolve,
reminder_seconds, config_ciphertext, display, created_at, updated_at
