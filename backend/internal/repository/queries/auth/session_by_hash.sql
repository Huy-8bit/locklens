UPDATE auth_sessions AS a
SET last_seen_at = now(), expires_at = greatest(a.expires_at, now() + $2::interval)
FROM users AS u
WHERE a.token_hash = $1 AND a.expires_at > now() AND u.id = a.user_id AND NOT u.disabled
RETURNING a.user_id, u.username, u.role, u.managed, a.csrf_token, a.expires_at
