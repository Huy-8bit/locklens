-- Index usage. idx_scan counts only scans since the last statistics reset
-- and says nothing about constraint enforcement, so the UI never suggests
-- dropping an index based on it.
SELECT i.indexrelid, i.relid, i.schemaname, i.relname, i.indexrelname, i.idx_scan,
       pg_catalog.pg_relation_size(i.indexrelid), x.indisunique, x.indisprimary,
       EXISTS (SELECT 1 FROM pg_catalog.pg_constraint AS k WHERE k.conindid = i.indexrelid)
FROM pg_catalog.pg_stat_user_indexes AS i
JOIN pg_catalog.pg_index AS x ON x.indexrelid = i.indexrelid
ORDER BY pg_catalog.pg_relation_size(i.indexrelid) DESC
LIMIT 300
