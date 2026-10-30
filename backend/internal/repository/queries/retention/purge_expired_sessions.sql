-- At most 5,000 rows per run; the caller repeats it until fewer are deleted.
DELETE FROM auth_sessions WHERE ctid IN (
    SELECT ctid FROM auth_sessions
    WHERE expires_at < $1
    LIMIT 5000)
