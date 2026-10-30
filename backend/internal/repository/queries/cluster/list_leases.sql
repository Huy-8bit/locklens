SELECT name, holder, acquired_at, expires_at, expires_at >= now() AS live
FROM leases WHERE name LIKE $1 || '%' ORDER BY name
