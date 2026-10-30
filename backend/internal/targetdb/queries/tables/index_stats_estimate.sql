SELECT i.indexrelid, i.relid, i.schemaname, i.relname, i.indexrelname, i.idx_scan,
       c.relpages::bigint * current_setting('block_size')::bigint, x.indisunique, x.indisprimary,
       EXISTS (SELECT 1 FROM pg_catalog.pg_constraint AS k WHERE k.conindid = i.indexrelid)
FROM pg_catalog.pg_stat_user_indexes AS i
JOIN pg_catalog.pg_index AS x ON x.indexrelid = i.indexrelid
JOIN pg_catalog.pg_class AS c ON c.oid = i.indexrelid
ORDER BY c.relpages DESC
LIMIT 300
