UPDATE findings SET status = 'resolved', resolved_at = $2
WHERE target_id = $1 AND status <> 'resolved'
  AND (rule_id, finding_key) NOT IN (SELECT r, k FROM unnest($3::text[], $4::text[]) AS u(r, k))
