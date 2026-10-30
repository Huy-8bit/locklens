UPDATE api_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL
