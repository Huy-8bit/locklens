SELECT k.conname, n.nspname, t.relname,
       ARRAY(SELECT a.attname FROM unnest(k.conkey) WITH ORDINALITY AS c(attnum, ord)
             JOIN pg_catalog.pg_attribute AS a ON a.attrelid = k.conrelid AND a.attnum = c.attnum ORDER BY c.ord),
       rn.nspname, r.relname, k.confdeltype::text, k.conrelid
FROM pg_catalog.pg_constraint AS k
JOIN pg_catalog.pg_class AS t ON t.oid = k.conrelid
JOIN pg_catalog.pg_namespace AS n ON n.oid = t.relnamespace
JOIN pg_catalog.pg_class AS r ON r.oid = k.confrelid
JOIN pg_catalog.pg_namespace AS rn ON rn.oid = r.relnamespace
WHERE k.contype = 'f' AND n.nspname NOT IN ('pg_catalog', 'information_schema')
LIMIT $1
