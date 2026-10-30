-- At most 5,000 rows per run; the caller repeats it until fewer are deleted.
DELETE FROM findings WHERE ctid IN (
    SELECT ctid FROM findings
    WHERE status = 'resolved' AND resolved_at < $1
    LIMIT 5000)
