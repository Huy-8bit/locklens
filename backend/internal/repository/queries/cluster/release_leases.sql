-- Gives up many leases held by $2 in one statement.
DELETE FROM leases WHERE name = ANY($1::text[]) AND holder = $2
