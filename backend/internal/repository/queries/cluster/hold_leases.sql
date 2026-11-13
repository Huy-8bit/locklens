-- Takes or renews many leases in one statement (as hold_lease.sql): each is
-- taken when free or expired, renewed when already held by $2. Returns the
-- names $2 holds afterwards. The caller sorts the names, so concurrent
-- servers lock rows in the same order and cannot deadlock.
INSERT INTO leases (name, holder, acquired_at, expires_at)
SELECT n, $2, now(), now() + make_interval(secs => $3)
FROM unnest($1::text[]) AS n
ON CONFLICT (name) DO UPDATE SET
    holder = EXCLUDED.holder,
    acquired_at = CASE WHEN leases.holder = EXCLUDED.holder THEN leases.acquired_at ELSE now() END,
    expires_at = EXCLUDED.expires_at
WHERE leases.holder = EXCLUDED.holder OR leases.expires_at < now()
RETURNING name
