INSERT INTO users (username, password_hash, role, disabled, managed) VALUES ($1, $2, 'admin', false, true)
ON CONFLICT (lower(username)) DO UPDATE SET password_hash = EXCLUDED.password_hash, role = 'admin', disabled = false, managed = true,
    updated_at = CASE WHEN users.password_hash = EXCLUDED.password_hash AND users.role = 'admin' AND NOT users.disabled AND users.managed
                      THEN users.updated_at ELSE now() END
RETURNING id
