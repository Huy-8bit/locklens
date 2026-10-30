SELECT id, ts, kind, summary, payload FROM incident_events
WHERE incident_id = $1 AND id > $2 ORDER BY ts, id LIMIT $3
