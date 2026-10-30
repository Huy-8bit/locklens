SELECT date_bin($4::interval, ts, 'epoch'::timestamptz), sum(covered_seconds), bool_or(reset_detected)
FROM statement_coverage
WHERE target_id = $1 AND ts >= $2 AND ts < $3
GROUP BY 1 ORDER BY 1
