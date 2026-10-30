WITH buckets AS (
 SELECT date_bin($5::interval, ts - interval '1 microsecond', $3::timestamptz) AS b, sum(calls) AS calls, sum(total_exec_ms) AS ms, sum(rows) AS rows, sum(interval_seconds) AS covered
 FROM query_samples WHERE target_id = $1 AND identity_id = $2 AND ts > $3 AND ts <= $4
 GROUP BY b
)
SELECT b,calls,ms,rows,covered,EXISTS(SELECT 1 FROM collection_gaps g WHERE g.target_id=$1 AND g.collector='statements' AND g.ended_at>b AND g.started_at<b+$5::interval)
FROM buckets ORDER BY b LIMIT 1001
