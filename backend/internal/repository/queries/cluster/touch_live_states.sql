-- Marks the published live views of targets whose view did not change as
-- current, without rewriting them. Fenced as save_live_states.sql ($4 lease
-- epochs, in the order of $1). Returns the touched IDs.
UPDATE live_state SET at = $3
WHERE server_id = $2 AND target_id IN (
    SELECT u.target_id
    FROM unnest($1::bigint[], $4::bigint[]) AS u (target_id, epoch)
    JOIN leases l ON l.name = 'target:' || u.target_id AND l.holder = $2 AND l.epoch = u.epoch
    FOR SHARE OF l)
RETURNING target_id
