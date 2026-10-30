UPDATE query_identities SET query_text = NULL WHERE target_id = $1 AND query_text IS NOT NULL
