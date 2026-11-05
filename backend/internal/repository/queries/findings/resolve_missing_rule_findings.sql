-- Resolves the active findings of one rule ($3) of a target that are not in
-- the given keys ($4), leaving the findings of other rules alone.
UPDATE findings SET status = 'resolved', resolved_at = $2
WHERE target_id = $1 AND rule_id = $3 AND status <> 'resolved'
  AND NOT finding_key = ANY ($4::text[])
