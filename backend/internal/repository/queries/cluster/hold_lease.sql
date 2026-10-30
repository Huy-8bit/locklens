-- Takes a lease that is free, expired, or already held by $2, for $3
-- seconds; returns no row while another server holds it.
INSERT INTO leases (name, holder, acquired_at, expires_at) VALUES ($1, $2, now(), now() + make_interval(secs => $3))
ON CONFLICT (name) DO UPDATE SET
    holder = EXCLUDED.holder,
    acquired_at = CASE WHEN leases.holder = EXCLUDED.holder THEN leases.acquired_at ELSE now() END,
    expires_at = EXCLUDED.expires_at
WHERE leases.holder = EXCLUDED.holder OR leases.expires_at < now()
RETURNING acquired_at
