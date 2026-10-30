-- The newest table health collection of every database, among those of the
-- hour before the newest one (a database that left the scope drops out).
WITH newest AS (
    SELECT max(ts) AS ts FROM state_snapshots WHERE target_id = $1 AND kind = 'table_health'
)
SELECT DISTINCT ON (coalesce(s.payload->>'datname', '')) s.ts, s.payload
FROM state_snapshots s, newest
WHERE s.target_id = $1 AND s.kind = 'table_health' AND s.ts >= newest.ts - interval '1 hour'
ORDER BY coalesce(s.payload->>'datname', ''), s.ts DESC
