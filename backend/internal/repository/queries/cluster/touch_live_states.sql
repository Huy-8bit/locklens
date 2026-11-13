-- Marks the published live views of targets whose view did not change as
-- current, without rewriting them.
UPDATE live_state SET at = $3 WHERE target_id = ANY($1::bigint[]) AND server_id = $2
