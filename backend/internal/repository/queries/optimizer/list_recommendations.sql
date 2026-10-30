SELECT id, target_id, datname, kind, rec_key, status, severity, title, summary, details, ddl, benefit, confidence,
first_seen, last_seen, decided_by, decided_at, note, applied_at, applied_how, before_ms, after_ms, verified_at, whatif, resolved_at FROM recommendations
WHERE ($1 = 0 OR target_id = $1) AND (cardinality($2::text[]) = 0 OR status = ANY ($2)) AND ($3 = '' OR kind = $3) AND ($5::text IS NULL OR datname = $5)
ORDER BY benefit DESC, id LIMIT $4
