DELETE FROM change_events WHERE id = $1 AND source <> 'detected'
RETURNING id, target_id, at, kind, source, title, details, created_by
