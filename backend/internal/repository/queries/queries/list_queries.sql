-- The workload list: one row per statement identity with its window sums,
-- the previous equal window when $4 (compare), and the total row count.
-- {{order}} {{direction}}: a sort key's expression (see QuerySorts).
WITH cur AS (
    SELECT identity_id, sum(calls) AS calls, sum(total_exec_ms) AS total_ms, sum(rows) AS rows,
           sum(shared_blks_hit) AS hit, sum(shared_blks_read) AS rd, sum(temp_blks_written) AS temp,
           sum(wal_bytes)::float8 AS wal
    FROM query_samples WHERE target_id = $1 AND ts > $2 AND ts <= $3 GROUP BY identity_id
), prev AS (
    SELECT identity_id, sum(calls) AS calls, sum(total_exec_ms) AS total_ms
    FROM query_samples WHERE $4::bool AND target_id = $1 AND ts > $5 AND ts <= $2 GROUP BY identity_id
), tot AS (SELECT sum(total_ms) AS t FROM cur)
SELECT i.id, i.server_major, i.datname, i.username, i.toplevel, i.queryid, i.command,
       CASE WHEN $6::bool THEN i.query_text END,
       cur.calls, cur.total_ms, cur.rows, cur.hit, cur.rd, cur.temp, cur.wal,
       cur.total_ms / NULLIF(tot.t, 0), prev.calls, prev.total_ms,
       count(*) OVER ()
FROM cur
JOIN query_identities AS i ON i.id = cur.identity_id
LEFT JOIN prev ON prev.identity_id = cur.identity_id
CROSS JOIN tot
WHERE ($7 = '' OR i.datname ILIKE '%' || $7 || '%' OR i.username ILIKE '%' || $7 || '%'
      OR i.queryid::text = $7 OR ($6::bool AND i.query_text ILIKE '%' || $7 || '%'))
  AND ($10 = '' OR i.username <> $10)
  AND ($11 = '' OR i.command = $11)
ORDER BY {{order}} {{direction}} NULLS LAST, i.id
LIMIT $8 OFFSET $9
