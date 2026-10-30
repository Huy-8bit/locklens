-- At most 5,000 rows per run; the caller repeats it until fewer are deleted.
DELETE FROM change_events WHERE id IN (
    SELECT id FROM change_events
    WHERE at < $1
    LIMIT 5000)
