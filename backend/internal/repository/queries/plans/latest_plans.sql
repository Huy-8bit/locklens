SELECT DISTINCT ON (identity_id) id, identity_id, captured_at, captured_by, source, format, plan, fingerprint, total_cost, note
FROM query_plans WHERE target_id = $1 AND identity_id = ANY ($2::bigint[])
ORDER BY identity_id, captured_at DESC
