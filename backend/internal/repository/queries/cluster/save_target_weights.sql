-- Stores the measured cost of many targets ($1 IDs, $2 collector seconds per
-- minute, same order); a manual override is kept. Targets deleted meanwhile
-- are skipped.
INSERT INTO target_weights (target_id, busy_per_min, updated_at)
SELECT u.target_id, u.busy, now()
FROM unnest($1::bigint[], $2::float8[]) AS u (target_id, busy)
JOIN targets t ON t.id = u.target_id
ORDER BY u.target_id
ON CONFLICT (target_id) DO UPDATE SET busy_per_min = EXCLUDED.busy_per_min, updated_at = EXCLUDED.updated_at
