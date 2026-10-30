WITH w AS (
 SELECT identity_id, sum(calls)::bigint AS calls, sum(total_exec_ms) AS ms, sum(rows)::bigint AS rows,
        sum(shared_blks_read)::bigint AS rd, sum(temp_blks_written)::bigint AS tw
 FROM query_rollups WHERE target_id = $1 AND ts >= $2 GROUP BY identity_id
), total AS (SELECT sum(ms) AS ms FROM w)
SELECT w.identity_id, q.server_major, q.dbid::bigint, q.userid::bigint, q.toplevel, q.queryid, q.datname, q.username, q.command,
       CASE WHEN $4 THEN q.query_text END, w.calls, w.ms, w.rows, w.rd, w.tw, coalesce(w.ms / NULLIF(total.ms, 0), 0)
FROM w JOIN query_identities q ON q.id = w.identity_id, total
ORDER BY w.ms DESC LIMIT $3
