INSERT INTO live_state (target_id, server_id, at, view) VALUES ($1, $2, $3, $4)
ON CONFLICT (target_id) DO UPDATE SET server_id = EXCLUDED.server_id, at = EXCLUDED.at, view = EXCLUDED.view
