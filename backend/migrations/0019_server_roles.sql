-- Node roles and a cheaper live_state, for clusters of many collectors.
--
-- servers.roles: what each server does (api, collector, jobs). Servers that
-- predate roles do not write the column and get the default: they do
-- everything, which is what they do. Safe while old and new servers share the
-- store during a rolling upgrade.
ALTER TABLE servers ADD COLUMN IF NOT EXISTS roles text[] NOT NULL DEFAULT '{api,collector,jobs}';

-- live_state holds each target's live view, rewritten every few seconds by
-- the server collecting it and rebuilt from memory at the next publish. It
-- needs no durability: unlogged, it costs no WAL. It is not replicated to
-- standbys of the store and is emptied after a crash of the store; it fills
-- again within one publish interval (5 s).
ALTER TABLE live_state SET UNLOGGED;
