-- Events per kind in buckets of $4 seconds.
SELECT to_timestamp(floor(extract(epoch FROM ts) / $4) * $4) AS bucket, kind, count(*)
FROM log_events
WHERE target_id = $1 AND ts >= $2 AND ts < $3
GROUP BY 1, 2
ORDER BY 1, 2
