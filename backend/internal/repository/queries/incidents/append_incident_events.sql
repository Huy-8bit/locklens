UPDATE incidents SET last_seen_at = greatest(last_seen_at, $2) WHERE id = $1
