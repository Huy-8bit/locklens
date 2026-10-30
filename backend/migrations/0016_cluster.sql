-- Several LockLens servers can share one monitoring store. Each server
-- registers itself and renews a heartbeat; collection of each instance and
-- each once-per-cluster job (retention, rollups, the optimizer) is held by
-- one server at a time through a lease that expires when the server stops
-- renewing it, so another server takes it over.
CREATE TABLE servers (
    id           text        PRIMARY KEY,
    hostname     text        NOT NULL,
    version      text        NOT NULL,
    started_at   timestamptz NOT NULL,
    heartbeat_at timestamptz NOT NULL
);

-- Lease names: "target:<id>" (collection of an instance) and "job:<name>".
CREATE TABLE leases (
    name        text        PRIMARY KEY,
    holder      text        NOT NULL,
    acquired_at timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);

-- The live view of an instance, published by the server that collects it
-- for the other servers' API requests (only while there is more than one).
CREATE TABLE live_state (
    target_id  bigint      PRIMARY KEY REFERENCES targets (id) ON DELETE CASCADE,
    server_id  text        NOT NULL,
    at         timestamptz NOT NULL,
    view       jsonb       NOT NULL
);
