INSERT INTO statement_collections (target_id, ts, interval_seconds, entries, changed, hidden, dealloc_delta, reset_detected)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT DO NOTHING
