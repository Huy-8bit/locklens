UPDATE recommendations SET status = 'applied', applied_at = $2, applied_how = $3, before_ms = $4
WHERE id = $1 AND status IN ('open', 'accepted', 'dismissed', 'resolved')
