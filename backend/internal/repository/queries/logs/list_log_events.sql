SELECT ts, event_id, kind, pid, datname, username, application, severity, sqlstate, duration_ms, query_id, fingerprint,
       query, message, details
FROM log_events
WHERE target_id = $1 AND ts >= $2 AND ts < $3
  AND (cardinality($4::text[]) = 0 OR kind = ANY ($4))
  AND ($5 = '' OR fingerprint = $5)
ORDER BY ts DESC, event_id
LIMIT $6
