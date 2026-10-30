INSERT INTO change_events (target_id, at, kind, source, title, details, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id
