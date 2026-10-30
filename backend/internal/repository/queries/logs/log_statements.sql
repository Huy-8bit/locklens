-- Logged executions per statement (fingerprint): the slow statement log
-- when the statement is in it, otherwise auto_explain (the same execution
-- can be in both, so they are not added up). Percentiles are of the logged
-- executions only.
WITH ev AS (
    SELECT fingerprint, kind, duration_ms, query, query_id, ts
    FROM log_events
    WHERE target_id = $1 AND ts >= $2 AND ts < $3 AND kind IN ('slow_query', 'plan')
      AND fingerprint IS NOT NULL AND duration_ms IS NOT NULL
), src AS (
    SELECT fingerprint, CASE WHEN bool_or(kind = 'slow_query') THEN 'slow_query' ELSE 'plan' END AS source,
           count(*) FILTER (WHERE kind = 'plan') AS plans
    FROM ev GROUP BY fingerprint
)
SELECT e.fingerprint, s.source, min(e.query), max(e.query_id), count(*), sum(e.duration_ms),
       percentile_cont(0.5) WITHIN GROUP (ORDER BY e.duration_ms),
       percentile_cont(0.95) WITHIN GROUP (ORDER BY e.duration_ms),
       percentile_cont(0.99) WITHIN GROUP (ORDER BY e.duration_ms),
       max(e.duration_ms), max(e.ts), s.plans
FROM ev e JOIN src s ON s.fingerprint = e.fingerprint AND e.kind = s.source
GROUP BY e.fingerprint, s.source, s.plans
ORDER BY sum(e.duration_ms) DESC
LIMIT $4
