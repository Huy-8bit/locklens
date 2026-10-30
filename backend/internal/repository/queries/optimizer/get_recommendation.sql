SELECT id, target_id, datname, kind, rec_key, status, severity, title, summary, details, ddl, benefit, confidence,
first_seen, last_seen, decided_by, decided_at, note, applied_at, applied_how, before_ms, after_ms, verified_at, whatif, resolved_at FROM recommendations WHERE target_id = $1 AND id = $2
