SELECT id, username, password_hash, role, disabled, managed, created_at, updated_at, last_login_at FROM users ORDER BY managed DESC, lower(username)
