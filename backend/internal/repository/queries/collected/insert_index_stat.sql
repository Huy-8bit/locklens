INSERT INTO index_stats (target_id, ts, indexrelid, relid, datname, schemaname, relname, indexrelname, idx_scan,
    index_bytes, is_unique, is_primary, is_constraint)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) ON CONFLICT DO NOTHING
