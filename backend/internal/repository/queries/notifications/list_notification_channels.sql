SELECT id, name, kind, enabled, min_severity, environments, on_open, on_escalate, on_resolve,
reminder_seconds, config_ciphertext, display, created_at, updated_at, rule_ids, target_ids, delay_seconds FROM notification_channels ORDER BY name
