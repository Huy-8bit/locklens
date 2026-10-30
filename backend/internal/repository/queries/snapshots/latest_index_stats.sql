SELECT indexrelid, relid, coalesce(datname, ''), schemaname, relname, indexrelname, idx_scan, index_bytes, is_unique, is_primary, is_constraint
FROM index_stats WHERE target_id = $1 AND ts = $2 ORDER BY index_bytes DESC NULLS LAST
