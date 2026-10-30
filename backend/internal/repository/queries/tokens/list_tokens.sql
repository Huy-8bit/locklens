SELECT id, name, scope, target_id, prefix, created_by, created_at, last_used_at, revoked_at
FROM api_tokens ORDER BY revoked_at IS NOT NULL, created_at DESC
