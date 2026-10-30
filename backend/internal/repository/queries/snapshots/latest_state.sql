SELECT ts, payload FROM state_snapshots WHERE target_id = $1 AND kind = $2 ORDER BY ts DESC LIMIT 1
