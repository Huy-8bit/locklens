-- At most 5,000 rows per run; the caller repeats it until fewer are deleted.
DELETE FROM targets WHERE ctid IN (
    SELECT ctid FROM targets
    WHERE deleted_at IS NOT NULL AND deleted_at < $1
      AND NOT EXISTS (SELECT 1 FROM incidents i WHERE i.target_id = targets.id)
    LIMIT 5000)
