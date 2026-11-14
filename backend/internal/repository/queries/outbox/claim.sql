-- Claims the outbox rows of up to $2 incidents for $1, oldest first. A row
-- is claimable when nobody claimed it, $1 did (its sender restarted), or its
-- claim is older than $3 seconds (the claimer stopped). All rows of an
-- incident are claimed together, and an incident with rows another sender
-- claims is skipped, so two senders never process one incident at once.
-- Two concurrent claims of the same rows serialise on the row locks; the
-- second re-checks the claim and skips them.
UPDATE notification_outbox o SET claimed_by = $1, claimed_at = now()
WHERE (o.claimed_at IS NULL OR o.claimed_by = $1 OR o.claimed_at < now() - make_interval(secs => $3))
  AND o.incident_id IN (
    SELECT p.incident_id FROM notification_outbox p
    WHERE (p.claimed_at IS NULL OR p.claimed_by = $1 OR p.claimed_at < now() - make_interval(secs => $3))
      AND NOT EXISTS (
        SELECT 1 FROM notification_outbox c
        WHERE c.incident_id = p.incident_id AND c.claimed_by <> $1 AND c.claimed_at >= now() - make_interval(secs => $3))
    GROUP BY p.incident_id
    ORDER BY min(p.id)
    LIMIT $2)
RETURNING o.id, o.incident_id, o.event, o.created_at
