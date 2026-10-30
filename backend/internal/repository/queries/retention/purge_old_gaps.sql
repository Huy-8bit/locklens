-- At most 5,000 rows per run; the caller repeats it until fewer are deleted.
DELETE FROM collection_gaps WHERE ctid IN (
    SELECT ctid FROM collection_gaps
    WHERE ended_at < $1
    LIMIT 5000)
