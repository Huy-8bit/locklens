UPDATE incidents SET state = $2, resolved_at = $3, resolution = $4, severity = $5, title = $6, truncated = truncated OR $7
WHERE id = $1
