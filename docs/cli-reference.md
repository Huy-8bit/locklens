# CLI reference

```text
locklens [global flags] <command> [flags]
```

Commands: [`doctor`](#locklens-doctor), [`inspect`](#locklens-inspect),
[`watch`](#locklens-watch), [`snapshot save`](#locklens-snapshot-save),
[`snapshot show`](#locklens-snapshot-show), [`version`](#locklens-version).
`locklens help [command]` and `--help` print built-in help.

## Connection and configuration

The connection string (DSN) is resolved in this order:

1. `--dsn`
2. the `LOCKLENS_DSN` environment variable

If neither is set, commands that connect exit with code 2. No configuration
file is read.

The DSN is any connection string pgx accepts: a URL
(`postgres://user@host:5432/db?sslmode=verify-full`) or key/value pairs
(`host=db user=observer dbname=app sslmode=verify-full`). Fields missing from
the DSN fall back to the standard libpq environment variables (`PGHOST`,
`PGPORT`, `PGUSER`, `PGPASSWORD`, `PGSSLMODE`, ...) and to `~/.pgpass`, so the
password does not have to be in the DSN at all.

> **Process list:** arguments such as `--dsn 'postgres://u:secret@...'` are
> visible to other local users (for example through `ps`). Prefer
> `LOCKLENS_DSN`, `PGPASSWORD`, or `~/.pgpass`.

TLS settings in the DSN are used exactly as pgx interprets them; LockLens never
changes `sslmode`.

## Global flags

| Flag | Default | Description |
| --- | --- | --- |
| `--dsn` | `$LOCKLENS_DSN` | PostgreSQL connection string |
| `--connect-timeout` | `5s` | Timeout for establishing a connection |
| `--query-timeout` | `10s` | Timeout for one collection; also sent to the server as `statement_timeout` |
| `--log-level` | `info` | Level of stderr logs: `debug`, `info`, `warn`, `error` |

Durations use Go syntax: `500ms`, `30s`, `5m`, `1h30m`.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success. Finding blocking is a successful result, not an error. `watch` also exits 0 after Ctrl+C or SIGTERM. |
| `1` | Runtime failure: connection, authentication, permission, collection, timeout, or I/O error; `doctor` with a failed check. |
| `2` | Invalid input: unknown command or flag, bad flag value, missing or unparsable DSN, `--out` file already exists without `--force`, missing/corrupt/unsupported snapshot file. |

Errors are printed to stderr as `locklens: error: ...`, often followed by a
`hint: ...` line.

## Output streams

- Reports, JSON, and NDJSON go to **stdout** only.
- Logs, retry messages, and errors go to **stderr**.

`--format json` output can therefore be piped directly into `jq` or another
parser.

## Output formats

- `table` (default): a human-readable report. No colours; terminal control
  sequences are used only by `watch` when stdout is a terminal. Every string
  from the server is sanitised before printing. Unknown values print as
  `unknown`.
- `json`: an indented JSON document. For `inspect`, `watch`, and
  `snapshot show` it is a report object:

  ```text
  {
    "snapshot": { raw data, see snapshot-format.md },
    "analysis": { summary, nodes, edges, components, sessions,
                  long_transactions, warnings }
  }
  ```

  The `analysis` fields are described in
  [snapshot-format.md](snapshot-format.md#report-json-analysis).

## `locklens doctor`

Checks whether LockLens can observe the server.

| Check | Fails / warns when |
| --- | --- |
| `connection` | fail: cannot connect or authenticate |
| `server version` | fail: PostgreSQL older than 14 |
| `read pg_stat_activity`, `read pg_locks`, `execute pg_blocking_pids()`, `read pg_prepared_xacts` | fail: access denied |
| `session visibility` | warn: role lacks `pg_read_all_stats`; prints the `GRANT` to run |
| `track_activities` | warn: setting is off |
| `query text` | warn: other roles' query text is hidden |
| `collect snapshot` | fail: a full collection fails; skipped if a source is unreadable |

| Flag | Default | Description |
| --- | --- | --- |
| `--format` | `table` | `table` or `json` |

Exit code 0 when no check failed (warnings allowed), 1 otherwise.

```bash
export LOCKLENS_DSN='postgres://locklens_observer:observer-pw@127.0.0.1:55432/locklens_demo?sslmode=disable'
locklens doctor
locklens doctor --format json | jq '.checks[] | select(.status != "ok")'
```

JSON shape: `{"target": "...", "ok": true, "checks": [{"name", "status", "detail", "hint"}]}`
where `status` is `ok`, `warn`, `fail`, or `skip`.

## `locklens inspect`

Collects one snapshot and prints the report: header (time, target, server),
summary, blocking chains with root blockers, blocked sessions, long-running
transactions, optional query text, and warnings.

| Flag | Default | Description |
| --- | --- | --- |
| `--format` | `table` | `table` or `json` |
| `--min-tx-age` | `1m` | Report client-backend transactions open at least this long (inclusive). `0s` lists every open transaction |
| `--include-query` | `false` | Collect SQL query text. Off by default because statements can contain sensitive data |
| `--query-max-length` | `200` | Maximum characters (runes) of query text kept per session, `1`–`1048576`. Only relevant with `--include-query` |

```bash
locklens inspect
locklens inspect --format json | jq '.analysis.components[] | {roots, members}'
locklens inspect --min-tx-age 30s --include-query --query-max-length 120
```

When nothing is blocked the report says `No blocking detected` and the command
exits 0.

## `locklens watch`

Repeats `inspect` until interrupted.

| Flag | Default | Description |
| --- | --- | --- |
| `--interval` | `2s` | Time between the *starts* of two collections; minimum `500ms` |
| `--format` | `table` | `table` or `json` (NDJSON) |
| `--min-tx-age` | `1m` | As for `inspect` |
| `--include-query` | `false` | As for `inspect` |
| `--query-max-length` | `200` | As for `inspect` |

Behaviour:

- **No overlap.** The next collection starts only after the previous one has
  finished: immediately if it took longer than `--interval`, otherwise when the
  interval since its start has elapsed.
- **Table on a terminal:** the screen is cleared before each frame.
- **Table redirected or piped:** no escape codes; frames are separated by
  `=== frame N ===` lines.
- **JSON:** NDJSON, one complete report object per line.
- **Failures:** a failed connection or collection is logged on stderr
  (`collection failed; retrying attempt=N retry_in=...`) and retried after
  `interval × 2^(N−1)`, capped at `max(30s, interval)`. The delay resets after
  a success. Authentication failures (`28000`, `28P01`), a missing database
  (`3D000`), permission errors (`42501`), and unsupported server versions stop
  the command with exit code 1 because retrying cannot help.
- **Ctrl+C / SIGTERM:** the current collection is cancelled and the command
  exits 0 without printing an error.

```bash
locklens watch
locklens watch --interval 5s --min-tx-age 30s
locklens watch --format json > incident.ndjson
locklens watch --format json | jq --unbuffered '.analysis.summary.blocked_sessions'
```

## `locklens snapshot save`

Collects one snapshot and writes the raw data as JSON.

| Flag | Default | Description |
| --- | --- | --- |
| `--out`, `-o` | (required) | Destination file |
| `--force` | `false` | Replace an existing file |
| `--min-tx-age` | `1m` | Stored in the file as the default threshold for `snapshot show` |
| `--include-query` | `false` | Store query text |
| `--query-max-length` | `200` | As for `inspect` |

- An existing `--out` is never replaced without `--force` (exit code 2). The
  check happens before connecting and again atomically when the file is
  placed.
- The file is written to a temporary file in the same directory, synced, and
  then moved into place, so a failure never leaves a truncated destination.
- The file mode is `0600`.
- The file never contains the DSN, password, or other credentials.

```bash
locklens snapshot save --out incident.json
locklens snapshot save -o incident.json --include-query --force
```

## `locklens snapshot show`

Renders a saved snapshot offline. No connection is made and no DSN is needed.

```text
locklens snapshot show FILE [flags]
```

| Flag | Default | Description |
| --- | --- | --- |
| `--format` | `table` | `table` or `json` |
| `--min-tx-age` | value stored in the file | Override the long-transaction threshold |

The file holds every client backend with an open transaction and every session
involved in blocking at collection time, so lowering the threshold can reveal
transactions that were below the original threshold, but never sessions that
were not recorded.

Errors (exit code 2): file does not exist, invalid JSON (with line number),
missing `schema_version`, or a `schema_version` this build does not support.

```bash
locklens snapshot show incident.json
locklens snapshot show incident.json --format json | jq '.analysis.summary'
locklens snapshot show docs/examples/snapshot-chain.json --min-tx-age 0s
```

## `locklens version`

Prints the version, commit, build time, and Go version. Values are injected at
build time (`make build` does this); otherwise Go's embedded VCS information is
used, and missing values print as `unknown`.

| Flag | Default | Description |
| --- | --- | --- |
| `--format` | `table` | `table` or `json` |

```bash
locklens version
locklens version --format json
```

## Environment variables

| Variable | Used by | Purpose |
| --- | --- | --- |
| `LOCKLENS_DSN` | all connecting commands | Connection string when `--dsn` is not given |
| `PGHOST`, `PGPORT`, `PGUSER`, `PGPASSWORD`, `PGDATABASE`, `PGSSLMODE`, `PGPASSFILE`, ... | pgx | Defaults for fields missing from the DSN |
| `LOCKLENS_DEMO_DSN` | `locklens-demo` only | Role that creates demo contention |
| `LOCKLENS_PG_VERSION`, `LOCKLENS_PG_PORT` | `docker compose`, `make` | Demo database image version and host port |
| `LOCKLENS_IT_DEMO_DSN`, `LOCKLENS_IT_OBSERVER_DSN`, `LOCKLENS_IT_LIMITED_DSN` | integration tests only | Override test connection strings |
