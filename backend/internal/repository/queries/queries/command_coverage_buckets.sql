SELECT date_bin($4::interval, ts, $2), sum(interval_seconds)
FROM statement_collections WHERE target_id = $1 AND ts > $2 AND ts <= $3 AND interval_seconds IS NOT NULL
GROUP BY 1 ORDER BY 1
