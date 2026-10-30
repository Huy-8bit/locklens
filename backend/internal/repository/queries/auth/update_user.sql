UPDATE users SET role = coalesce($2, role), disabled = coalesce($3, disabled), password_hash = coalesce($4, password_hash), updated_at = now()
WHERE id = $1 RETURNING id, username, password_hash, role, disabled, managed, created_at, updated_at, last_login_at
