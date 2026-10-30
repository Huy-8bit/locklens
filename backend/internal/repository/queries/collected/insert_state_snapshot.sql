INSERT INTO state_snapshots (target_id, kind, ts, payload) VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING
