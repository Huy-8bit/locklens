# Security

This page describes how LockLens protects the databases it observes, the
credentials it holds, and the data it collects, and what operators are
responsible for. To report a vulnerability, see [SECURITY.md](../SECURITY.md).

## Principles

- **Observe only.** LockLens never changes a monitored database: no DDL or
  DML, no `VACUUM`, no index changes, no configuration changes, no session
  cancellation or termination, no failover. Findings may *suggest* what a
  person could check; LockLens never acts on them.
- **Least privilege.** A login role with `pg_read_all_stats` is enough; a
  superuser is never needed.
- **Secrets stay secret.** Connection strings and passwords are never returned
  by the API, shown in the UI, written to logs, exports, snapshots, or metric
  labels.
- **SQL text is collected by default** and can be switched off per target;
  table rows are readable only through an opt-in, non-administrative role.
- **Local by default.** The server binds to `127.0.0.1:8080` unless told
  otherwise.

## Monitored databases

Every connection from the CLI and the server:

- sets `default_transaction_read_only=on` and runs each collection in a
  `READ ONLY` transaction, so the server itself refuses writes;
- sets `statement_timeout`, `lock_timeout` (2 s), and
  `idle_in_transaction_session_timeout` (60 s), so LockLens cannot hold locks
  or leave transactions open;
- uses a small pool (1–5 connections per target, default 2) and runs one
  collector at a time per target;
- identifies itself as `application_name = locklens` and excludes its own
  PIDs from reports.

Expensive operations (database and relation sizes) run every 5 minutes with a
250 ms lock timeout and fall back to estimates, so a long DDL statement on the
target never makes LockLens wait.

`make check-separation` (part of `make check` and CI) fails if the `locklens`
binary depends on any demo or lab package, the only code that writes to
databases (`demo/`).

Recommended role:

```sql
CREATE ROLE locklens_observer LOGIN PASSWORD '...';
GRANT pg_read_all_stats TO locklens_observer;
-- Workload analysis (optional), in the database LockLens connects to:
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
```

Use `sslmode=verify-full` with `sslrootcert` over untrusted networks. TLS
settings in the DSN are honoured and never weakened.

## Stored credentials

- Target DSNs are encrypted with **AES-256-GCM** using `LOCKLENS_MASTER_KEY`
  (32 random bytes, base64), with a random nonce per value and associated data
  binding the ciphertext to its purpose. The key comes only from the
  environment; it is never stored in the database or logged.
- Only display fields (host, port, database, user) are stored in clear text.
- The API accepts a DSN on create, update, and test, and never returns it. The
  UI's DSN field is write-only ("Leave empty to keep the stored connection
  string").
- Connection errors pass through a redactor that removes the exact DSN and
  password (including URL-encoded forms) before they reach the UI, the API,
  or logs. A DSN that cannot be parsed is never echoed.
- Bootstrap targets take their DSN from an environment variable named in the
  file, so the file holds no secrets.

## Authentication and roles

- Users have one of three roles: **administrator** (everything), **operator**
  (reads everything, previews rows, runs read-only SQL, acknowledges
  incidents), and **viewer** (reads monitoring data and table structure only).
  The API checks the permission of every mutating route and of each workspace
  action (`metadata`, `data`, `sql`/`explain`); the UI only hides what a role
  cannot use. A missing permission answers `403`.
- The environment administrator (`LOCKLENS_ADMIN_USER`) cannot be changed or
  deleted through the API; nobody can change their own role, disable, or delete
  themselves.
- Passwords (12–72 bytes) are stored as bcrypt hashes (cost 12); the
  environment administrator's can also be supplied as a hash
  (`LOCKLENS_ADMIN_PASSWORD_HASH`, created with `locklens admin hash-password`).
  A disabled user cannot sign in; the answer is the same as for a wrong
  password.
- Login compares in constant time and runs bcrypt even for unknown users, so
  response times do not reveal user names. Five failed attempts per client per
  minute are allowed; then `429` with `Retry-After`.
- Sessions are server-side. The browser holds a random 256-bit token in the
  `locklens_session` cookie (`HttpOnly`, `SameSite=Strict`, `Secure` unless the
  server listens on loopback); the store keeps only its SHA-256 hash. Sessions
  expire after `LOCKLENS_SESSION_IDLE` without activity; logout deletes them.
  Changing a user's password or disabling them ends their sessions; the role is
  read on every request.
- No tokens are kept in `localStorage` or `sessionStorage`. The CSRF token is
  held in memory by the UI.
- **API tokens** (Settings → API tokens) are for machines only: scope
  `events` reports changes, scope `ingest` ships log events and belongs to one
  instance. A token is 24 random bytes (`llk_…`), shown once; the store keeps
  its SHA-256 hash. Token requests carry no cookie and need no CSRF header;
  a token can do nothing else (no reads, no other endpoints). Revoking takes
  effect at once.

## Log agent

- `locklens agent` reads log files only and never connects to PostgreSQL.
  It takes its token from `LOCKLENS_AGENT_TOKEN` or a file, never from a
  flag, and refuses a non-local `http://` server unless `--allow-http`.
- Before sending, it replaces the constants of statements (also in deadlock
  reports) with `$n`, constants in plan conditions with `?`, and quoted values
  in error messages with `"?"`, and drops error DETAIL lines (which carry key
  values). Values the server writes unquoted in a message are not
  recognised. Its spool and read positions are files with mode 0600 in
  `--state-dir`.

## Web application protections

- **CSRF**: mutations require the session's CSRF token in `X-CSRF-Token`, and
  requests whose `Origin` names another host are rejected. Combined with
  `SameSite=Strict`, cross-site requests cannot act as the administrator.
- **Same origin only**: no CORS headers are sent.
- **Headers**: `Content-Security-Policy: default-src 'self'; script-src 'self';
  style-src 'self'; img-src 'self' data:; connect-src 'self';
  frame-ancestors 'none'; base-uri 'none'; form-action 'self'`,
  `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
  `Referrer-Policy: no-referrer`, `Cache-Control: no-store` on API responses.
- **Input limits**: JSON bodies 1 MiB, uploads 10 MiB, time ranges ≤ 31 days,
  pages ≤ 200 rows, metric name and filter lengths, validated sort keys. All
  SQL is parameterised.
- **Errors**: internal errors return a generic message and a request ID; the
  details stay in the server log.
- **Escaping**: React escapes all rendered values; the HTML export uses Go's
  `html/template`. Strings controlled by database clients (`application_name`,
  query text) are stripped of control and bidirectional characters before they
  are printed by the CLI.

## Query text and personal data

SQL text can contain personal data or secrets in literals. LockLens therefore:

- collects it by default so the workload pages are useful, and lets the
  administrator switch it off per target (**Collect SQL text**);
- returns it only to authenticated requests;
- excludes it from exports unless explicitly requested;
- never puts it in logs, error messages, or Prometheus labels;
- deletes a target's stored statement texts when the option is turned off.

Other collected values are still sensitive operational data: user, database,
and application names, client addresses, table names, and timings. Protect
the monitoring store and exports accordingly.

## Imports

Incident imports and snapshot uploads are limited to 10 MiB and validated
(format, schema version, UTF-8, field lengths, event counts) before anything
is stored or rendered. Unknown versions are rejected.

## Host metrics

A node_exporter URL must be `http` or `https` without embedded credentials.
Responses are limited to 8 MiB, parsed as Prometheus text, and only derived
numbers are stored. Only the administrator can set the URL; since the server
will fetch it, do not expose the administrator account to untrusted users.

## Operational endpoints

`/healthz`, `/readyz`, and `/metrics` are unauthenticated so probes and
Prometheus can reach them. They expose no credentials, query text, or user
input; metric labels are bounded (collector names, outcomes, target IDs).
Disable `/metrics` with `LOCKLENS_METRICS_ENABLED=false` if needed.

## Operator responsibilities

- Keep the default loopback bind, or put the server behind a TLS-terminating
  reverse proxy and set `LOCKLENS_COOKIE_SECURE=true`. LockLens does not
  terminate TLS itself.
- Generate a real master key (`openssl rand -base64 32`), keep it out of
  version control, and back it up separately from the store.
- Use a strong administrator password, ideally supplied as a hash.
- Use dedicated, least-privileged monitoring roles and TLS to targets.
- Restrict network access to the monitoring store.
- The Docker lab's passwords and key are fake, local-only values. Never reuse
  them.

## Not provided

- SSO, per-environment permissions, or a general audit log (workspace
  operations are audited per user).
- In-place master key rotation (re-enter DSNs; see
  [storage-and-retention.md](storage-and-retention.md#rotating-the-master-key)).
- Encryption of the monitoring store at rest beyond the DSN column (use
  disk or database-level encryption).
- Built-in TLS termination.

## Investigation access

Metadata, application rows and explicit SQL are independent, default-deny target
capabilities with separate credentials. The current administrator can configure these
capabilities per target; roles decide who may use them (viewers never read rows or run
SQL). SQL uses PostgreSQL read-only transactions,
limits, disposable connections and session-owned cancellation. This is not a sandbox
for arbitrary function side effects. Review the dedicated role's SELECT, ownership,
PUBLIC and function grants. Workspace audits do not store raw SQL/results. Optional
browser history has a separate opt-in policy. See [the full access model](investigation-workspace.md).

The web UI's **SQL Editor** is the client of that SQL capability; it is off for every
target until an administrator enables "Read-only SQL editor" (with a confirmation) or a
bootstrap file sets `workspace.sql`. The Docker lab enables it for its two lab targets,
whose data is fake. The editor keeps drafts in page memory only and offers no way to
terminate sessions, cancel other backends, change settings, or write.

## Adding an instance with a superuser connection string

When an administrator pastes a superuser connection string under **Settings →
Add instance**, LockLens shows the exact statements it would run and, only
after confirmation, connects once to create two roles on that primary:

- `locklens_monitor`: `LOGIN`, member of `pg_monitor`, for monitoring;
- `locklens_reader`: `LOGIN`, member of `pg_read_all_data`,
  `default_transaction_read_only = on`, for the Explorer and SQL editor.

Both are created (or reset) with `NOSUPERUSER NOCREATEDB NOCREATEROLE
NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 10` and a generated 192-bit
password, and get `CONNECT` on the database. The passwords are encrypted with
the master key and stored per cluster (system identifier), so standbys reuse
them. The superuser connection string is never stored, logged, or returned,
and nothing else is run with it. This is the only case in which LockLens writes
to a monitored server, and only on an explicit administrator action; collectors
remain read-only. If the roles already exist, their passwords are reset, which
breaks anything else that used them; the plan says so before confirmation.

A non-superuser connection string is used as is. The Explorer and SQL editor
refuse roles with superuser, `CREATEROLE`, `CREATEDB`, `BYPASSRLS`, or
server-file privileges, because a single `SELECT` could otherwise call
functions such as `pg_terminate_backend` or `pg_read_file`.

## Frontend and backend images

The frontend (nginx, unprivileged) serves the UI with the same headers the
backend sets: `Content-Security-Policy` allowing only `'self'`,
`X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`,
`Referrer-Policy: no-referrer`, and `Cache-Control: no-store` on the document;
it does not advertise its version. It proxies `/api/` to the backend with the
browser's `Host`, so the backend's CSRF `Origin` check and `SameSite=Strict`
cookie work as on a single server. Publish only the frontend; the backend's
`/metrics`, `/healthz`, and `/readyz` are unauthenticated and meant for the
internal network.

Login rate limits are per client address. Behind the frontend, set
`LOCKLENS_TRUSTED_PROXIES` to the frontend's host name or address only: the
backend then uses the right-most `X-Forwarded-For` address that is not a
trusted proxy, and ignores the header from anyone else. Trusting whole client
networks would let clients on them choose their address.

