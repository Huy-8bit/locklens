SELECT name, holder, acquired_at, expires_at, expires_at >= now() AS live, epoch
FROM leases WHERE name LIKE $1 || '%' ORDER BY name
