UPDATE recommendations SET status = $3, decided_by = $4, decided_at = now(), note = $5
WHERE target_id = $1 AND id = $2 AND status IN ('open', 'accepted', 'dismissed')
