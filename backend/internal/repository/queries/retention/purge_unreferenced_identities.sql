-- At most 5,000 rows per run; the caller repeats it until fewer are deleted.
DELETE FROM query_identities WHERE ctid IN (
    SELECT ctid FROM query_identities
    WHERE NOT EXISTS (SELECT 1 FROM query_rollups r WHERE r.identity_id = query_identities.id)
      AND NOT EXISTS (SELECT 1 FROM query_plans p WHERE p.identity_id = query_identities.id)
      AND last_seen < $1
      AND NOT EXISTS (SELECT 1 FROM query_samples q WHERE q.identity_id = query_identities.id)
    LIMIT 5000)
