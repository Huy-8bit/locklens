INSERT INTO query_plans (target_id, identity_id, captured_by, source, format, plan, fingerprint, total_cost, note)
SELECT $1, q.id, $3, $4, $5, $6, $7, $8, $9 FROM query_identities q WHERE q.id = $2 AND q.target_id = $1
RETURNING id, captured_at
