# Configuration

`locklens server` is configured only through environment variables. The CLI
commands (`inspect`, `watch`, `snapshot`, `doctor`) keep their flags and
`LOCKLENS_DSN`; see [cli-reference.md](cli-reference.md).

Invalid values stop the server at startup with a message naming the variable.
All problems are reported at once.

## Required

| Variable | Description |
| --- | --- |
| `LOCKLENS_STORE_DSN` | Connection string of LockLens' own monitoring store: a dedicated PostgreSQL 14+ database. Never point it at a database you monitor. The role needs to create tables in that database on first start (migrations). |
| `LOCKLENS_MASTER_KEY` | 32 random bytes, base64-encoded. Encrypts stored target connection strings (AES-256-GCM). Generate with `openssl rand -base64 32`. It is never written to the store; losing it makes stored DSNs unreadable (re-enter them). |
| `LOCKLENS_ADMIN_PASSWORD` or `LOCKLENS_ADMIN_PASSWORD_HASH` | Needed on the first start to create the administrator, optional later. See [Administrator](#administrator). |

## Optional

| Variable | Default | Description |
| --- | --- | --- |
| `LOCKLENS_LISTEN` | `127.0.0.1:8080` (`0.0.0.0:8080` in the backend image) | `host:port` the API listens on. Expose it to the frontend only; put TLS in front of the frontend. `locklens server --listen` overrides it. |
| `LOCKLENS_TRUSTED_PROXIES` | unset | Comma-separated IPs, CIDRs, or host names of reverse proxies (the frontend) whose `X-Forwarded-For` names the client for login rate limits and logs; host names are resolved every 30 seconds. Unset: the connecting address is used. Compose sets `frontend`. See [deployment.md](deployment.md#settings-that-connect-the-two). |
| `LOCKLENS_ADMIN_USER` | `admin` | Administrator user name. |
| `LOCKLENS_COOKIE_SECURE` | `auto` | `auto` sets the session cookie's `Secure` flag unless the listen address is loopback (plain HTTP on loopback would never send a Secure cookie). Set `true` behind an HTTPS proxy, `false` only for local plain-HTTP setups such as the Docker lab. |
| `LOCKLENS_SESSION_IDLE` | `12h` | A session ends after this much inactivity (minimum `5m`). Activity extends it. |
| `LOCKLENS_STORE_TIMESCALE` | `auto` | Use TimescaleDB in the monitoring store: `auto` when the store's server has the extension (preloaded), `on` to refuse to start without it, `off` for plain tables. See [storage-and-retention.md](storage-and-retention.md#timescaledb). |
| `LOCKLENS_STORE_COMPRESS_AFTER` | `24h` | Compress history chunks older than this (TimescaleDB Community edition); `0` turns compression off (minimum `1h`). |
| `LOCKLENS_RETENTION` | `168h` (7 days) | History retention: metric points, statement samples and collections, activity snapshots, table and index statistics, state snapshots (minimum `1h`). |
| `LOCKLENS_INCIDENT_RETENTION` | `2160h` (90 days) | Incidents and their events are kept this long after they resolve (minimum `24h`). Open incidents are never deleted. |
| `LOCKLENS_BASELINE_RETENTION` | `360h` (15 days) | Five-minute statement rollups and statement coverage, the source of query baselines (minimum `24h`; never shorter than `LOCKLENS_RETENTION`). See [query-intelligence.md](query-intelligence.md). |
| `LOCKLENS_FINDING_RETENTION` | `720h` (30 days) | Resolved findings and collection-gap records (minimum `1h`). Incidents keep their own copy of the evidence, so this never changes an incident. |
| `LOCKLENS_MAX_CONCURRENT_COLLECTIONS` | `4` | Global cap on collector runs executing at the same time across all targets (1–64). Each target also runs its own collectors one at a time. |
| `LOCKLENS_LOAD_SAMPLING` | `true` | Active session sampling (database load): read `pg_stat_activity` every `LOCKLENS_LOAD_SAMPLE_INTERVAL` on each target (PostgreSQL 14+). |
| `LOCKLENS_LOAD_SAMPLE_INTERVAL` | `2s` | Time between two samples (`1s`–`1m`). Shorter catches shorter bursts and costs one more small query per target per interval. |
| `LOCKLENS_PUBLIC_URL` | (none) | The address users open LockLens at (`http` or `https`, without a query string). Notifications link to incidents with it; without it they carry no link. |
| `LOCKLENS_WRITE_QUEUE` | `2048` | Capacity of the in-memory queue of batches waiting to be written to the store (16–100000). When the store is unavailable and the queue is full, the oldest batch is dropped and a collection gap is recorded. |
| `LOCKLENS_METRICS_ENABLED` | `true` | Serve Prometheus metrics at `/metrics` (unauthenticated, bounded labels, no query text). |
| `LOCKLENS_BOOTSTRAP_TARGETS_FILE` | unset | JSON file of targets to create at startup. See [Bootstrap targets](#bootstrap-targets). |
| `LOCKLENS_DEMO_MODE` | `false` | Lab mode: lower rule thresholds and shorter persistence so scenarios trigger within minutes, statements collected every 15 s instead of 60 s, and the Lab guide page enabled. Do not use in production. |
| `LOCKLENS_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `LOCKLENS_LOG_FORMAT` | `text` | `text` or `json` (stderr). Logs never contain DSNs, passwords, or query text. |
| `LOCKLENS_SHUTDOWN_TIMEOUT` | `10s` | Grace period for in-flight requests and collectors on SIGINT/SIGTERM. |

## Users and the environment administrator

LockLens has users with one of three roles:

| Role | Can |
| --- | --- |
| Administrator | everything: users, instances, investigation access, rule thresholds, incident import |
| Operator | read everything, preview table rows, run read-only SQL, acknowledge incidents |
| Viewer | read dashboards, queries, sessions, topology, and incidents; browse table structure |

The **environment administrator** comes from `LOCKLENS_ADMIN_USER` and
`LOCKLENS_ADMIN_PASSWORD`. It is always an enabled administrator and cannot be
changed or deleted in the UI, so access can always be recovered by restarting
with a new password. Other users are added under **Settings → Users and roles** (or
`POST /api/v1/users`); users change their own password under **Account**.

- On the first start, set `LOCKLENS_ADMIN_PASSWORD` (12 to 72 bytes) or
  `LOCKLENS_ADMIN_PASSWORD_HASH` (a bcrypt hash). The password is hashed with
  bcrypt (cost 12); the plain value is never stored.
- Create a hash without putting the password in your shell history:

  ```sh
  locklens admin hash-password   # reads the password from stdin
  ```

- On later starts both variables may be omitted; the stored account is used.
- If the configured password differs from the stored one, the stored hash is
  replaced and **that administrator's sessions are ended**. This is also how
  you reset a forgotten password. Changing `LOCKLENS_ADMIN_USER` replaces the
  previous environment administrator; other users stay.
- A role change applies to the user's next request. Disabling a user or
  resetting their password ends their sessions.
- Five failed logins per client per minute are allowed; further attempts are
  rejected until the window passes.

## Targets

Targets are normally added in the UI from a single connection string
(**Settings → Instances → Add instance**, see
[investigation-workspace.md](investigation-workspace.md#adding-an-instance-from-one-connection-string))
or through the API ([api.md](api.md)). The advanced form sets every option.
Per target:

| Setting | Default | Range | Notes |
| --- | --- | --- | --- |
| Name | required | 1–100 characters, unique (case-insensitive) | |
| DSN | required | | Encrypted at rest; never returned by the API. Leave empty on edit to keep it. |
| Activity interval | 10 s | 5–3600 s | Other collectors derive their intervals from it; see [collectors.md](collectors.md). |
| Maximum connections | 2 | 1–5 | Size of the target's connection pool. |
| SQL text | on | | Collected by default. Turning it off deletes the stored statement texts of that target. |
| node_exporter URL | unset | `http(s)://` | Optional host metrics. |
| Enabled | on | | Disabled targets keep their history but are not contacted. |

Use a dedicated login role with `pg_read_all_stats` (or `pg_monitor`); a
superuser is not needed. See
[postgresql-observation.md](postgresql-observation.md#required-privileges).

### Bootstrap targets

`LOCKLENS_BOOTSTRAP_TARGETS_FILE` points to a JSON array. Each entry is
created at startup **only if no target with that name exists**; existing
targets are never changed. The DSN comes from the environment variable named
in `dsn_env`, so the file itself holds no credentials:

```json
[
  {
    "name": "orders-primary",
    "description": "Orders database primary",
    "dsn_env": "ORDERS_PRIMARY_DSN",
    "interval_seconds": 10,
    "max_connections": 2,
    "query_text_enabled": true,
    "environment": "production",
    "workspace": { "metadata": true, "data": false, "sql": false, "dsn_env": "ORDERS_PRIMARY_READER_DSN" }
  }
]
```

A missing variable stops startup with an error naming it. `environment` groups
targets in the UI's environment selector (lowercase letters, digits, `-`, `_`;
default `default`). The optional
`workspace` block enables the Explorer for that target with a separate
role (`dsn_env` names its DSN; same host, port, and database, a different
SELECT-only user): `metadata` allows browsing structure, `data` allows
reading rows, and `sql` enables the read-only SQL editor. It is applied only while the target has no workspace
configuration yet; a failure (for example an unreachable server) is logged and
does not stop startup. The Docker lab uses
[demo/targets.json](../demo/targets.json).

## Rule thresholds

Rule thresholds are edited in **Settings → Alert rules** (or
`PUT /api/v1/settings/rules`) and stored in the monitoring store. They apply
from the next evaluation. Defaults and meanings are in [rules.md](rules.md).

## Example: production-like start

```sh
export LOCKLENS_STORE_DSN='postgres://locklens_store@store.internal:5432/locklens?sslmode=verify-full'
export LOCKLENS_MASTER_KEY="$(cat /run/secrets/locklens_master_key)"
export LOCKLENS_ADMIN_PASSWORD_HASH="$(cat /run/secrets/locklens_admin_hash)"
export LOCKLENS_LISTEN=127.0.0.1:8080        # TLS terminates in a reverse proxy
export LOCKLENS_COOKIE_SECURE=true
export LOCKLENS_LOG_FORMAT=json
locklens server
```

Run `locklens migrate` first if you want schema changes applied as a separate
deployment step; `locklens server` applies pending migrations itself too (under
an advisory lock, so several instances starting together are safe).

## Frontend image

The frontend image has one setting:

| Variable | Default | Description |
| --- | --- | --- |
| `LOCKLENS_BACKEND_URL` | `http://backend:8080` | Where nginx proxies `/api/`: `scheme://host:port` of the backend, without a path. It must resolve when the container starts. |

See [deployment.md](deployment.md).

