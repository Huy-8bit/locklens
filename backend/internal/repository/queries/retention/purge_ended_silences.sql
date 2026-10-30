-- At most 5,000 rows per run; the caller repeats it until fewer are deleted.
DELETE FROM notification_silences WHERE ctid IN (
    SELECT ctid FROM notification_silences
    WHERE ends_at < $1
    LIMIT 5000)
