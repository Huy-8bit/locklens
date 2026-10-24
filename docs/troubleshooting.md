# Troubleshooting

Start with `locklens doctor`: it tests the connection, version, privileges, and
a full collection, and prints a hint for each problem. Add `--log-level debug`
for more stderr output. LockLens never prints your password, so it is safe to
share its output, but check snapshot files before sharing them.

## Connection

### `no connection string: pass --dsn or set LOCKLENS_DSN` (exit 2)

Neither `--dsn` nor `LOCKLENS_DSN` is set. Export the variable or pass the
flag. An empty or whitespace-only value counts as unset.

### `invalid connection string (the value is not shown to protect credentials)` (exit 2)

The DSN could not be parsed. LockLens deliberately does not echo it. Check:

- URL form: special characters in the password must be percent-encoded
  (`p@ss` becomes `p%40ss`); IPv6 hosts need brackets (`[::1]`).
- Key/value form: values with spaces need single quotes
  (`password='a b'`); quotes must be closed.
- Numeric fields (`port`, `connect_timeout`) must be numbers.

Diagnose without exposing the password by moving it to `PGPASSWORD` and
testing the rest of the DSN.

### `connection refused`

Nothing listens on that host and port. Check the server is running, the port,
`listen_addresses` on the server, and firewalls. For the demo: `docker compose
ps` and `make db-up`.

### `the host name could not be resolved`

DNS failure. Check the spelling, VPN, or use an IP address.

### `the operation timed out`

The server did not answer within `--connect-timeout` (default 5s) or a
collection exceeded `--query-timeout` (default 10s). Check network paths and
server load; raise the timeouts if the server is legitimately slow. A
collection timeout on a healthy server usually means a very large number of
waiting backends.

### `pg_hba.conf rejects this client`

The server refuses the combination of client address, user, database, and SSL
mode. The DBA needs to add a matching `pg_hba.conf` entry.

### `the server has no free connection slots`

`max_connections` is exhausted. LockLens needs one connection (at most two).
Ask the DBA for `superuser_reserved_connections`/`reserved_connections`
headroom or retry when load drops.

## Authentication

### `authentication failed` (exit 1)

Wrong user or password. LockLens uses the password from the DSN, then
`PGPASSWORD`, then `~/.pgpass` (or `PGPASSFILE`). In `watch`, authentication failures
stop the command instead of retrying.

### `the database does not exist`

The database name in the DSN is wrong. Any existing database works because
LockLens' sources are cluster-wide; relation names are resolved only for the
database you connect to.

## Permissions

### Report shows `[details hidden by permissions]` or warning `restricted_visibility`

The role lacks `pg_read_all_stats`. Blocking relations are still correct, but
state, timings, and query text of other roles' sessions are hidden, and their
long transactions are invisible. Fix:

```sql
GRANT pg_read_all_stats TO your_role;
```

Then reconnect. See
[postgresql-observation.md](postgresql-observation.md#required-privileges).

### `permission denied` in doctor checks

A deployment has revoked access to `pg_stat_activity`, `pg_locks`,
`pg_blocking_pids()`, or `pg_prepared_xacts` from `PUBLIC`. Grant access to
the LockLens role explicitly, or use a role with `pg_monitor`.

### Query text shows `[hidden by permissions]` with `--include-query`

Same cause as above: only roles with `pg_read_all_stats` see other roles' query
text.

### Warning `track_activities_off`

`track_activities` is off for the server or for those sessions. State, wait
events, and query text are not maintained. Only a superuser can change it.

## TLS

### `TLS negotiation failed`

- `sslmode=require` or stricter but the server does not offer TLS: enable TLS
  on the server or, for local tests only, use `sslmode=disable`.
- `sslmode=verify-ca`/`verify-full` and `x509: certificate signed by unknown
  authority`: pass the CA with `sslrootcert=/path/ca.pem`.
- `verify-full` and a host name mismatch: connect using a name that is in the
  certificate.

LockLens never lowers `sslmode`. The default when unspecified is `prefer`,
which tries TLS first and falls back to an unencrypted connection, as in libpq.
This is why a refused connection may be reported twice per address.

## Timeouts and slow collections

- `a query was cancelled by statement_timeout`: the collection exceeded
  `--query-timeout`. Raise it.
- `LockLens could not obtain a catalog lock within lock_timeout`: something
  holds a strong lock on a system catalog LockLens reads (for example an
  `ACCESS EXCLUSIVE` lock taken by a maintenance command). That session is
  likely your incident; check it with another tool or wait.

## Docker and the demo

- `Cannot connect to the Docker daemon`: start Docker Desktop, OrbStack, or the
  docker service.
- `port is already allocated` for 55432: another service uses the port. Run
  `make db-up LOCKLENS_PG_PORT=55433` and set `LOCKLENS_DSN` /
  `LOCKLENS_DEMO_DSN` / `LOCKLENS_IT_*_DSN` to the new port.
- The container keeps restarting after changing `LOCKLENS_PG_VERSION`: an old
  data volume from another major version is being reused. Run `make db-down`
  (or use `make db-up`, which renews anonymous volumes).
- Integration tests fail with `PREPARE TRANSACTION` errors: the server runs
  without `max_prepared_transactions`; use the provided compose file.
- `scenario: timed out after 15s waiting for ...`: the expected lock state
  never appeared, usually because the database is overloaded or another test
  run is using the same tables. Run one demo or test suite at a time.
- `run locklens: ... no such file or directory`: build first (`make build`) or
  pass `--locklens /path/to/locklens` to `locklens-demo`.

## Snapshots

| Message | Cause | Fix |
| --- | --- | --- |
| `... already exists (use --force to overwrite)` (exit 2) | `--out` exists | Choose another name or add `--force` |
| `snapshot file X does not exist` (exit 2) | Wrong path | Check the path |
| `X is not a valid snapshot file: line N: ...` (exit 2) | Truncated or edited JSON | Restore the file; LockLens writes atomically, so partial files usually come from copying |
| `not a LockLens snapshot: schema_version is missing` (exit 2) | Not a snapshot file (for example `inspect --format json` output, which wraps the snapshot under `"snapshot"`) | Use a file written by `snapshot save`, or extract `.snapshot` with `jq` |
| `unsupported snapshot schema_version N` (exit 2) | Written by a newer LockLens | Upgrade LockLens |
| `create temporary file: ... permission denied` (exit 1) | Directory not writable | Choose a writable directory |
