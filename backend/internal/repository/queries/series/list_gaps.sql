SELECT collector, started_at, ended_at, reason FROM collection_gaps
WHERE target_id = $1 AND ended_at > $2 AND started_at <= $3 ORDER BY ended_at DESC LIMIT $4
