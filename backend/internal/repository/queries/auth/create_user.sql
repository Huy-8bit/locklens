INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id, username, password_hash, role, disabled, managed, created_at, updated_at, last_login_at
