-- Changes of the given instances (or all for an empty list) in a period,
-- with the changes that concern every instance; the newest first.
SELECT id, target_id, at, kind, source, title, details, created_by
FROM change_events
WHERE at >= $1 AND at < $2
  AND (cardinality($3::bigint[]) = 0 OR target_id = ANY ($3) OR target_id IS NULL)
  AND (cardinality($4::text[]) = 0 OR kind = ANY ($4))
ORDER BY at DESC, id DESC
LIMIT $5
