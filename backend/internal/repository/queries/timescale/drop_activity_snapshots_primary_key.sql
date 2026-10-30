ALTER TABLE activity_snapshots DROP CONSTRAINT IF EXISTS activity_snapshots_pkey, ADD PRIMARY KEY (id, ts)
