UPDATE recommendations SET status = 'resolved', resolved_at = $2
WHERE target_id = $1 AND datname = $5 AND status = 'open'
  AND (kind, rec_key) NOT IN (SELECT k, r FROM unnest($3::text[], $4::text[]) AS u(k, r))
