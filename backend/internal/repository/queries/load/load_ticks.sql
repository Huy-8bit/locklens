SELECT date_bin($4::interval, ts, $2::timestamptz), sum(ticks)::bigint
FROM load_buckets WHERE target_id = $1 AND ts >= $2 AND ts < $3
GROUP BY 1 ORDER BY 1
