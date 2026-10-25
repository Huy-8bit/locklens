-- An environment label groups targets (for example production, staging, lab)
-- so the UI can show one environment at a time.
ALTER TABLE targets ADD COLUMN environment text NOT NULL DEFAULT 'default'
    CHECK (environment ~ '^[a-z0-9][a-z0-9_-]{0,39}$');
