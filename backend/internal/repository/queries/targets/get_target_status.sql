SELECT target_id, state, server_version, server_version_num, is_standby, capabilities, collectors,
last_attempt_at, last_success_at, last_error, last_error_at, updated_at FROM target_status WHERE target_id = $1
