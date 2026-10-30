SELECT id, name, description, dsn_ciphertext, display_host, display_port, display_database,
display_user, enabled, interval_seconds, query_text_enabled, max_connections, node_exporter_url,
created_at, updated_at, deleted_at, environment, database_scope, databases FROM targets WHERE deleted_at IS NULL ORDER BY lower(name)
