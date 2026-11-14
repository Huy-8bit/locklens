-- save_target_status.sql under a lease fence: written only while $12 holds
-- the target's lease with epoch $13 (share locked until the statement ends).
-- Returns no row when the lease was lost.
INSERT INTO target_status (target_id, state, server_version, server_version_num, is_standby, capabilities,
    collectors, last_attempt_at, last_success_at, last_error, last_error_at, updated_at)
SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now()
WHERE EXISTS (SELECT 1 FROM leases WHERE name = 'target:' || $1::bigint AND holder = $12 AND epoch = $13 FOR SHARE)
ON CONFLICT (target_id) DO UPDATE SET state = EXCLUDED.state, server_version = EXCLUDED.server_version,
    server_version_num = EXCLUDED.server_version_num, is_standby = EXCLUDED.is_standby,
    capabilities = EXCLUDED.capabilities, collectors = EXCLUDED.collectors,
    last_attempt_at = EXCLUDED.last_attempt_at, last_success_at = EXCLUDED.last_success_at,
    last_error = EXCLUDED.last_error, last_error_at = EXCLUDED.last_error_at, updated_at = now()
RETURNING target_id
