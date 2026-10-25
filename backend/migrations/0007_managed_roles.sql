-- Least-privilege login roles that LockLens created on a monitored cluster
-- when an administrator added it with an administrative connection string.
-- The administrative credentials are never stored; only these generated
-- passwords, encrypted with the master key. Physical standbys share the
-- primary's roles, so the credentials are keyed by the cluster's system
-- identifier and reused when another node of the same cluster is added.
CREATE TABLE managed_roles (
    system_identifier   text        NOT NULL,
    purpose             text        NOT NULL CHECK (purpose IN ('monitor', 'reader')),
    role_name           text        NOT NULL,
    password_ciphertext bytea       NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (system_identifier, purpose)
);
