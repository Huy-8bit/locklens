SELECT id, identity_id, captured_at, captured_by, source, format, plan, fingerprint, total_cost, note
FROM query_plans WHERE target_id = $1 AND id = $2
