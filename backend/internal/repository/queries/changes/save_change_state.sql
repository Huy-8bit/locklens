INSERT INTO change_state (target_id, kind, observed_at, state) VALUES ($1, $2, $3, $4)
ON CONFLICT (target_id, kind) DO UPDATE SET observed_at = EXCLUDED.observed_at, state = EXCLUDED.state
