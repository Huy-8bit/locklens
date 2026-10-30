-- Load samples summed per step bucket, wait class, and dimension value.
-- {{key}}: the dimension's columns as text[]; {{group}}: those grouped by
-- (see LoadDimensions). $5 (wait class) and $6 (queryid) filter when set.
SELECT date_bin($4::interval, ts, $2::timestamptz), wait_class, {{key}}, sum(samples)::bigint
FROM load_samples
WHERE target_id = $1 AND ts >= $2 AND ts < $3
  AND ($5::text IS NULL OR wait_class = $5)
  AND ($6::bigint IS NULL OR queryid = $6)
GROUP BY 1, wait_class{{group}}
ORDER BY 1
LIMIT 200000
