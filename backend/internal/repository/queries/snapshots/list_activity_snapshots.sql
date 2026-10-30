SELECT id, ts, blocked_sessions, root_blockers, long_transactions FROM activity_snapshots
WHERE target_id = $1 AND ts > $2 AND ts <= $3 AND (NOT $4 OR blocked_sessions > 0)
ORDER BY ts DESC LIMIT $5
