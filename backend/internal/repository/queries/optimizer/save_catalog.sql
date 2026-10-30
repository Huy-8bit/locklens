INSERT INTO catalog_state (target_id, datname, collected_at, catalog) VALUES ($1, $2, $3, $4)
ON CONFLICT (target_id, datname) DO UPDATE SET collected_at = EXCLUDED.collected_at, catalog = EXCLUDED.catalog
