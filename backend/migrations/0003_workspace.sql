CREATE TABLE workspace_policies (
 target_id bigint PRIMARY KEY REFERENCES targets(id),
 metadata_enabled boolean NOT NULL DEFAULT false,
 data_enabled boolean NOT NULL DEFAULT false,
 sql_enabled boolean NOT NULL DEFAULT false,
 history_enabled boolean NOT NULL DEFAULT false,
 dsn_ciphertext bytea,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE workspace_audit (
 id bigserial PRIMARY KEY,
 target_id bigint NOT NULL REFERENCES targets(id),
 actor text NOT NULL,
 action text NOT NULL,
 status text NOT NULL,
 duration_ms bigint NOT NULL,
 row_count integer NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE topology_links (
 primary_id bigint NOT NULL REFERENCES targets(id),
 standby_id bigint PRIMARY KEY REFERENCES targets(id),
 application_name text NOT NULL,
 provenance text NOT NULL,
 CHECK (primary_id <> standby_id)
);
