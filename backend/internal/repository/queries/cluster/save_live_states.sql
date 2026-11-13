-- Publishes the live views of many targets in one statement ($1 target IDs,
-- $4 views as JSON text, same order).
INSERT INTO live_state (target_id, server_id, at, view)
SELECT u.target_id, $2, $3, u.view::jsonb
FROM unnest($1::bigint[], $4::text[]) AS u (target_id, view)
ON CONFLICT (target_id) DO UPDATE SET server_id = EXCLUDED.server_id, at = EXCLUDED.at, view = EXCLUDED.view
