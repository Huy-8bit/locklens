-- Fencing: the lease $1 still has holder $2 and epoch $3. The row is share
-- locked until the transaction ends, so no other server takes the lease
-- over before the writes that follow commit.
SELECT 1 FROM leases WHERE name = $1 AND holder = $2 AND epoch = $3 FOR SHARE
