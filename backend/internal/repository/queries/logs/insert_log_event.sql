INSERT INTO log_events (target_id, ts, event_id, kind, pid, datname, username, application, severity, sqlstate,
    duration_ms, query_id, fingerprint, query, message, details)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT DO NOTHING
