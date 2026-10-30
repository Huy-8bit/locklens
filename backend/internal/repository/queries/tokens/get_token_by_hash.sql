SELECT id, name, scope, target_id, prefix, created_by, created_at, last_used_at, revoked_at
FROM api_tokens WHERE token_hash = $1 AND revoked_at IS NULL
