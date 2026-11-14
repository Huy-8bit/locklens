-- Cost-based assignment, lease fencing, and a notification outbox, for
-- clusters of many collectors. Add-only: servers that predate it keep
-- working while new ones share the store during a rolling upgrade.

-- Each holding of a lease gets a new epoch, from one sequence, so an epoch
-- is never reused (also after a lease was released and taken again). A
-- server writes incidents, findings, statuses, and live views of a target
-- only while the lease still has its holder and epoch, in the same
-- transaction. Older servers do not set it: a lease they insert gets a new
-- epoch from the default, one they take over keeps its epoch but changes its
-- holder, which fencing checks too. Rewrites the (small) leases table once.
CREATE SEQUENCE IF NOT EXISTS lease_epoch;
ALTER TABLE leases ADD COLUMN IF NOT EXISTS epoch bigint NOT NULL DEFAULT nextval('lease_epoch');

-- The cost of collecting a target: an exponentially weighted average of the
-- collector time it takes per minute (seconds), kept by the server
-- collecting it. weight_override, when set, is used instead (for a target
-- known to be expensive). Targets without a row cost the average.
CREATE TABLE IF NOT EXISTS target_weights (
    target_id       bigint           PRIMARY KEY REFERENCES targets (id) ON DELETE CASCADE,
    busy_per_min    double precision,
    weight_override double precision CHECK (weight_override IS NULL OR weight_override > 0),
    updated_at      timestamptz      NOT NULL DEFAULT now()
);

-- What each collector can carry (concurrent collections), the collector time
-- per minute of the targets it collects, and its target limit (NULL: none).
-- NULL for servers that predate them.
ALTER TABLE servers ADD COLUMN IF NOT EXISTS capacity double precision;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS load double precision;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS max_targets integer;

-- Incident changes to notify about, written in the same transaction as the
-- change by the server recording the incident and sent by the server
-- running the notification job. A row stays until it was processed, so a
-- change is never lost when a server stops; processing decides from the
-- incident's state and delivery history, so nothing is sent twice.
CREATE TABLE IF NOT EXISTS notification_outbox (
    id          bigserial   PRIMARY KEY,
    incident_id bigint      NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    event       text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    claimed_by  text,
    claimed_at  timestamptz
);
CREATE INDEX IF NOT EXISTS notification_outbox_incident ON notification_outbox (incident_id);
