INSERT INTO target_status (target_id, state, server_version, server_version_num, is_standby, capabilities,
    collectors, last_attempt_at, last_success_at, last_error, last_error_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
ON CONFLICT (target_id) DO UPDATE SET state = EXCLUDED.state, server_version = EXCLUDED.server_version,
    server_version_num = EXCLUDED.server_version_num, is_standby = EXCLUDED.is_standby,
    capabilities = EXCLUDED.capabilities, collectors = EXCLUDED.collectors,
    last_attempt_at = EXCLUDED.last_attempt_at, last_success_at = EXCLUDED.last_success_at,
    last_error = EXCLUDED.last_error, last_error_at = EXCLUDED.last_error_at, updated_at = now()
