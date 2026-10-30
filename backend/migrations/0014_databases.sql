-- Monitoring several databases of one instance. A target names which
-- databases LockLens connects to for per-database data (tables, indexes,
-- catalog, recommendations): only the one in its connection string
-- ("connected", the default), every database it may connect to ("all"), or a
-- list ("list"). Cluster-wide data (sessions, statements, replication) is
-- collected once per instance either way.
ALTER TABLE targets
    ADD COLUMN database_scope text NOT NULL DEFAULT 'connected' CHECK (database_scope IN ('connected', 'all', 'list')),
    ADD COLUMN databases text[] NOT NULL DEFAULT '{}';

-- Index OIDs are unique per database only.
ALTER TABLE index_stats ADD COLUMN datname text;

-- One catalog per database.
ALTER TABLE catalog_state ADD COLUMN datname text NOT NULL DEFAULT '';
UPDATE catalog_state SET datname = coalesce(catalog->>'datname', '');
ALTER TABLE catalog_state DROP CONSTRAINT catalog_state_pkey;
ALTER TABLE catalog_state ADD PRIMARY KEY (target_id, datname);

-- Recommendations belong to a database.
ALTER TABLE recommendations ADD COLUMN datname text NOT NULL DEFAULT '';
UPDATE recommendations r SET datname = coalesce(c.catalog->>'datname', '')
FROM catalog_state c WHERE c.target_id = r.target_id;
ALTER TABLE recommendations DROP CONSTRAINT recommendations_target_id_kind_rec_key_key;
ALTER TABLE recommendations ADD CONSTRAINT recommendations_key UNIQUE (target_id, datname, kind, rec_key);
