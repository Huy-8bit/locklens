INSERT INTO incidents (target_id, target_name, rule_id, rule_version, severity, title, state, opened_at,
    last_seen_at, resolved_at, resolution, trigger_config, truncated, source)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING id
