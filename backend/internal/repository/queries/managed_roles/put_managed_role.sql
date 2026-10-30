INSERT INTO managed_roles (system_identifier, purpose, role_name, password_ciphertext) VALUES ($1, $2, $3, $4)
ON CONFLICT (system_identifier, purpose) DO UPDATE SET role_name = EXCLUDED.role_name,
    password_ciphertext = EXCLUDED.password_ciphertext, updated_at = now()
