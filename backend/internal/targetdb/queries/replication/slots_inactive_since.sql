-- inactive_since exists from PostgreSQL 17.
SELECT slot_name, inactive_since FROM pg_catalog.pg_replication_slots
