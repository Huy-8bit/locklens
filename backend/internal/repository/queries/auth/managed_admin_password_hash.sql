SELECT password_hash FROM users WHERE lower(username) = lower($1)
