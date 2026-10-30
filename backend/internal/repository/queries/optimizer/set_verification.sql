UPDATE recommendations SET status = $2, after_ms = $3, verified_at = $4
WHERE id = $1 AND status IN ('applied', 'verified', 'no_effect', 'regressed')
