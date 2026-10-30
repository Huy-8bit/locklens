SELECT rolname FROM pg_roles WHERE rolname = ANY($1) ORDER BY rolname
