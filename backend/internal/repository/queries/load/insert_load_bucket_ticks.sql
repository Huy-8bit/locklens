INSERT INTO load_buckets (target_id, ts, ticks, interval_seconds) VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING
