INSERT INTO metric_points (target_id, metric, label, ts, value, interval_seconds)
SELECT $1, m, l, $2, v, i FROM unnest($3::text[], $4::text[], $5::float8[], $6::float8[]) AS u(m, l, v, i)
ON CONFLICT DO NOTHING
