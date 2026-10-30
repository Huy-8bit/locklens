SELECT x.indexrelid, x.indrelid, n.nspname, t.relname, i.relname, am.amname,
       ARRAY(SELECT coalesce(a.attname, '') FROM unnest(x.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord)
             LEFT JOIN pg_catalog.pg_attribute AS a ON a.attrelid = x.indrelid AND a.attnum = k.attnum AND k.attnum > 0
             ORDER BY k.ord),
       x.indnkeyatts, x.indisunique, x.indisprimary, x.indisvalid, x.indpred IS NOT NULL, x.indexprs IS NOT NULL,
       EXISTS (SELECT 1 FROM pg_catalog.pg_constraint AS k WHERE k.conindid = x.indexrelid),
       pg_catalog.pg_get_indexdef(x.indexrelid), i.relpages::bigint * current_setting('block_size')::bigint, s.idx_scan
FROM pg_catalog.pg_index AS x
JOIN pg_catalog.pg_class AS i ON i.oid = x.indexrelid
JOIN pg_catalog.pg_class AS t ON t.oid = x.indrelid
JOIN pg_catalog.pg_namespace AS n ON n.oid = t.relnamespace
JOIN pg_catalog.pg_am AS am ON am.oid = i.relam
LEFT JOIN pg_catalog.pg_stat_user_indexes AS s ON s.indexrelid = x.indexrelid
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_toast%'
LIMIT $1
