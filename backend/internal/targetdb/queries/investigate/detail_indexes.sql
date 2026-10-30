-- At most 200 rows are shown (201 are read to tell that there are more).
SELECT c.relname AS name,pg_get_indexdef(i.indexrelid) AS definition,pg_relation_size(i.indexrelid) AS bytes,s.idx_scan,s.idx_tup_read,s.idx_tup_fetch FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid LEFT JOIN pg_stat_user_indexes s ON s.indexrelid=i.indexrelid WHERE i.indrelid=$1 ORDER BY c.relname LIMIT 201
