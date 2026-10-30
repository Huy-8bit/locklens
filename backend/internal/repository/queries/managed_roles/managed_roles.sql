SELECT system_identifier, purpose, role_name, password_ciphertext FROM managed_roles WHERE system_identifier = $1
