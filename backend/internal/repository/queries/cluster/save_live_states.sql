-- Publishes the live views of many targets in one statement ($1 target IDs,
-- $4 views as JSON text, $5 lease epochs, same order). Fenced: a view is
-- written only while $2 holds the target's lease with that epoch (the lease
-- row is share locked until the statement ends). Returns the written IDs.
INSERT INTO live_state (target_id, server_id, at, view)
SELECT u.target_id, $2, $3, u.view::jsonb
FROM unnest($1::bigint[], $4::text[], $5::bigint[]) AS u (target_id, view, epoch)
JOIN leases l ON l.name = 'target:' || u.target_id AND l.holder = $2 AND l.epoch = u.epoch
FOR SHARE OF l
ON CONFLICT (target_id) DO UPDATE SET server_id = EXCLUDED.server_id, at = EXCLUDED.at, view = EXCLUDED.view
RETURNING target_id
