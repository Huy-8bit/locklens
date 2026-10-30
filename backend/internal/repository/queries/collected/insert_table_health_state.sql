INSERT INTO state_snapshots (target_id, kind, ts, payload) VALUES ($1, 'table_health', $2, $3)
ON CONFLICT DO NOTHING
