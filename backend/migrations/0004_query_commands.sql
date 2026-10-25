-- Statement command class (select, insert, update, ...), derived from the
-- normalised text at collection time and stored even when the text is not.
-- Existing identities are classified as 'other' until they are seen again.
ALTER TABLE query_identities ADD COLUMN command text NOT NULL DEFAULT 'other';
CREATE INDEX query_identities_target_command ON query_identities (target_id, command);

-- Query text collection is on by default for every target (it can still be
-- switched off per target).
ALTER TABLE targets ALTER COLUMN query_text_enabled SET DEFAULT true;
UPDATE targets SET query_text_enabled = true WHERE NOT query_text_enabled;
