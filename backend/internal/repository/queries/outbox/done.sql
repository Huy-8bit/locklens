-- Removes processed rows still claimed by $2.
DELETE FROM notification_outbox WHERE id = ANY($1::bigint[]) AND claimed_by = $2
