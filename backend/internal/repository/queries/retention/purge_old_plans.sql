-- At most 5,000 rows per run; the caller repeats it until fewer are deleted.
DELETE FROM query_plans WHERE ctid IN (
    SELECT ctid FROM query_plans
    WHERE captured_at < $1
    LIMIT 5000)
