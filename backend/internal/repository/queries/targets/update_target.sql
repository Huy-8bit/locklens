UPDATE targets SET name = $2, description = $3, dsn_ciphertext = $4, display_host = $5, display_port = $6,
    display_database = $7, display_user = $8, enabled = $9, interval_seconds = $10,
    query_text_enabled = $11, max_connections = $12, node_exporter_url = $13, environment = $14,
    database_scope = $15, databases = $16, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING id, name, description, dsn_ciphertext, display_host, display_port, display_database,
display_user, enabled, interval_seconds, query_text_enabled, max_connections, node_exporter_url,
created_at, updated_at, deleted_at, environment, database_scope, databases
