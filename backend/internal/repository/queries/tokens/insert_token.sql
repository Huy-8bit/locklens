INSERT INTO api_tokens (name, scope, target_id, token_hash, prefix, created_by) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, name, scope, target_id, prefix, created_by, created_at, last_used_at, revoked_at
