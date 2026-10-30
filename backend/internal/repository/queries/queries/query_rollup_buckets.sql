WITH cut AS (
 SELECT coalesce(max(ts) + interval '5 minutes', date_bin('5 minutes', $3::timestamptz, 'epoch'::timestamptz)) AS c
 FROM statement_coverage WHERE target_id = $1 AND ts >= $3::timestamptz - interval '5 minutes'
), act AS (
 SELECT r.ts AS b, r.calls, r.total_exec_ms AS ms, r.rows FROM query_rollups r, cut
 WHERE r.target_id = $1 AND r.identity_id = $2 AND r.ts >= date_bin('5 minutes', $3::timestamptz, 'epoch'::timestamptz) AND r.ts < cut.c
 UNION ALL
 SELECT date_bin('5 minutes', ts - interval '1 microsecond', 'epoch'::timestamptz), sum(calls), sum(total_exec_ms), sum(rows)
 FROM query_samples, cut WHERE target_id = $1 AND identity_id = $2 AND ts > greatest(cut.c, $3::timestamptz) AND ts <= $4 GROUP BY 1
), cov AS (
 SELECT c.ts AS b, c.covered_seconds AS covered FROM statement_coverage c, cut
 WHERE c.target_id = $1 AND c.ts >= date_bin('5 minutes', $3::timestamptz, 'epoch'::timestamptz) AND c.ts < cut.c
 UNION ALL
 SELECT date_bin('5 minutes', ts - interval '1 microsecond', 'epoch'::timestamptz), least(sum(interval_seconds), 300)
 FROM statement_collections, cut WHERE target_id = $1 AND interval_seconds IS NOT NULL AND ts > greatest(cut.c, $3::timestamptz) AND ts <= $4 GROUP BY 1
), a AS (
 SELECT date_bin($5::interval, b, $3::timestamptz) AS bucket, sum(calls)::bigint AS calls, sum(ms) AS ms, sum(rows)::bigint AS rows FROM act GROUP BY 1
), c AS (
 SELECT date_bin($5::interval, b, $3::timestamptz) AS bucket, sum(covered) AS covered FROM cov GROUP BY 1
)
SELECT a.bucket, a.calls, a.ms, a.rows, coalesce(c.covered, 0),
 EXISTS (SELECT 1 FROM collection_gaps g WHERE g.target_id = $1 AND g.collector = 'statements' AND g.ended_at > a.bucket AND g.started_at < a.bucket + $5::interval)
FROM a LEFT JOIN c USING (bucket) ORDER BY a.bucket LIMIT 1001
