SELECT id, identity_id, captured_at, captured_by, source, format, '', fingerprint, total_cost, note
FROM query_plans WHERE target_id = $1 AND identity_id = $2 ORDER BY captured_at DESC LIMIT 200
