SELECT id, ts, blocked_sessions, root_blockers, long_transactions, snapshot FROM activity_snapshots
WHERE target_id = $1 AND id = $2
