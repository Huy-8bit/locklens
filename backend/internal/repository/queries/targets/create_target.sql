INSERT INTO targets (name, description, dsn_ciphertext, display_host, display_port, display_database,
    display_user, enabled, interval_seconds, query_text_enabled, max_connections, node_exporter_url, environment,
    database_scope, databases)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING id, name, description, dsn_ciphertext, display_host, display_port, display_database,
display_user, enabled, interval_seconds, query_text_enabled, max_connections, node_exporter_url,
created_at, updated_at, deleted_at, environment, database_scope, databases
