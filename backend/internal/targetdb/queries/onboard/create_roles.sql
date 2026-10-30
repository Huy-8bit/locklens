-- The statements that create (or reset) LockLens' two least-privilege roles,
-- shown to the administrator before they run. The attributes are explicit so
-- an existing role is brought back to least privilege. Placeholders are
-- filled by queries.CreateRoles: {{monitor_verb}} and {{reader_verb}} are
-- CREATE or ALTER, roles and the database are quoted identifiers, passwords
-- quoted literals. One statement per line.
{{monitor_verb}} ROLE {{monitor}} WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 10 PASSWORD {{monitor_password}}
GRANT pg_monitor TO {{monitor}}
{{reader_verb}} ROLE {{reader}} WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 10 PASSWORD {{reader_password}}
GRANT pg_read_all_data TO {{reader}}
ALTER ROLE {{reader}} SET default_transaction_read_only = on
GRANT CONNECT ON DATABASE {{database}} TO {{monitor}}, {{reader}}
