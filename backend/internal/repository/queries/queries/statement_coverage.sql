SELECT coalesce(sum(interval_seconds), 0), count(*), count(*) FILTER (WHERE reset_detected),
       coalesce(sum(dealloc_delta), 0), coalesce(max(hidden), 0)
FROM statement_collections WHERE target_id = $1 AND ts > $2 AND ts <= $3
