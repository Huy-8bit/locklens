-- At most 5,000 rows per run; the caller repeats it until fewer are deleted.
DELETE FROM incidents WHERE ctid IN (
    SELECT ctid FROM incidents
    WHERE state = 'resolved' AND resolved_at < $1
    LIMIT 5000)
