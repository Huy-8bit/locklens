-- At most 5,000 rows per run; the caller repeats it until fewer are deleted.
DELETE FROM recommendations WHERE ctid IN (
    SELECT ctid FROM recommendations
    WHERE status IN ('resolved', 'dismissed') AND coalesce(resolved_at, decided_at, last_seen) < $1
    LIMIT 5000)
