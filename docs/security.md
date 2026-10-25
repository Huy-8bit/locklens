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
  table rows are readable only through a separate, opt-in role.
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

## Administrator authentication

- One administrator. The password (12–72 bytes) is stored as a bcrypt hash
  (cost 12), or supplied directly as a hash (`LOCKLENS_ADMIN_PASSWORD_HASH`,
  created with `locklens admin hash-password`).
- Login compares in constant time and runs bcrypt even for unknown users, so
  response times do not reveal user names. Five failed attempts per client per
  minute are allowed; then `429` with `Retry-After`.
- Sessions are server-side. The browser holds a random 256-bit token in the
  `locklens_session` cookie (`HttpOnly`, `SameSite=Strict`, `Secure` unless the
  server listens on loopback); the store keeps only its SHA-256 hash. Sessions
  expire after `LOCKLENS_SESSION_IDLE` without activity; logout deletes them.
  Changing the configured password ends all sessions.
- No tokens are kept in `localStorage` or `sessionStorage`. The CSRF token is
  held in memory by the UI.

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

- Multiple users, roles, SSO, or audit logs (single administrator only).
- In-place master key rotation (re-enter DSNs; see
  [storage-and-retention.md](storage-and-retention.md#rotating-the-master-key)).
- Encryption of the monitoring store at rest beyond the DSN column (use
  disk or database-level encryption).
- Built-in TLS termination.

## Investigation access

Metadata, application rows and explicit SQL are independent, default-deny target
capabilities with separate credentials. The current administrator can configure these
capabilities; they are not per-user RBAC. SQL uses PostgreSQL read-only transactions,
limits, disposable connections and session-owned cancellation. This is not a sandbox
for arbitrary function side effects. Review the dedicated role's SELECT, ownership,
PUBLIC and function grants. Workspace audits do not store raw SQL/results. Optional
browser history has a separate opt-in policy. See [the full access model](investigation-workspace.md).

The web UI's **SQL Editor** is the client of that SQL capability; it is off for every
target until an administrator enables "Read-only SQL editor" (with a confirmation) or a
bootstrap file sets `workspace.sql`. The Docker lab enables it for its two lab targets,
whose data is fake. The editor keeps drafts in page memory only and offers no way to
terminate sessions, cancel other backends, change settings, or write.
