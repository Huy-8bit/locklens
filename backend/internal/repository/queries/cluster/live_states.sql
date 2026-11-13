-- Published live views of many targets (fleet pages), in one statement.
SELECT target_id, server_id, at, view FROM live_state WHERE target_id = ANY($1::bigint[])
