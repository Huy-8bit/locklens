# LockLens

PostgreSQL monitoring and investigation in one small image: what your
databases are doing now (connections, queries by type, sessions, blocking,
replication), how they got there (history, incidents with evidence), and
opt-in, read-only data browsing and SQL.

Source, documentation, and issues: https://github.com/Huy-8bit/locklens

## Quick start

LockLens keeps its history in its own PostgreSQL database (the *store*), never
in the databases it monitors.

```sh
# Generate the master key once and keep it (a password manager, a secret store).
export LOCKLENS_MASTER_KEY="$(openssl rand -base64 32)"

docker network create locklens

docker run -d --name locklens-store --network locklens \
  -e POSTGRES_USER=locklens -e POSTGRES_PASSWORD=change-me -e POSTGRES_DB=locklens \
  -v locklens-store:/var/lib/postgresql \
  postgres:18-alpine

docker run -d --name locklens --network locklens \
  -p 127.0.0.1:8080:8080 \
  -e LOCKLENS_LISTEN=0.0.0.0:8080 \
  -e LOCKLENS_STORE_DSN='postgres://locklens:change-me@locklens-store:5432/locklens?sslmode=disable' \
  -e LOCKLENS_MASTER_KEY \
  -e LOCKLENS_ADMIN_PASSWORD='choose-a-long-password' \
  -e LOCKLENS_COOKIE_SECURE=false \
  johnnynguyen882/locklens:latest
```

Open http://127.0.0.1:8080, sign in as `admin`, and add a PostgreSQL instance
under **Settings** with a read-only monitoring role (`pg_read_all_stats` or
`pg_monitor`; no superuser needed).

Keep the master key: it encrypts the stored connection strings, and a new key
makes them unreadable. `LOCKLENS_COOKIE_SECURE=false` is only for plain HTTP
on your own machine; behind HTTPS, leave it unset.

## Configuration

| Variable | Required | Description |
| --- | --- | --- |
| `LOCKLENS_STORE_DSN` | yes | PostgreSQL 14+ database for LockLens' own data |
| `LOCKLENS_MASTER_KEY` | yes | 32 random bytes, base64 (`openssl rand -base64 32`) |
| `LOCKLENS_ADMIN_PASSWORD` or `LOCKLENS_ADMIN_PASSWORD_HASH` | first start | Creates the single administrator (password 12–72 bytes, or a bcrypt hash) |
| `LOCKLENS_LISTEN` | in a container | Default `127.0.0.1:8080`; use `0.0.0.0:8080` inside a container |
| `LOCKLENS_ADMIN_USER` | no | Default `admin` |
| `LOCKLENS_RETENTION` | no | History retention, default `168h` |
| `LOCKLENS_BOOTSTRAP_TARGETS_FILE` | no | JSON file of instances to register at startup |
| `LOCKLENS_LOG_FORMAT` | no | `text` (default) or `json` |

Full list: [configuration](https://github.com/Huy-8bit/locklens/blob/main/docs/configuration.md).

## Image details

- Port `8080`. Health: `GET /healthz` (liveness), `GET /readyz` (store
  reachable). Prometheus metrics at `/metrics`.
- Runs as the non-root user `10001`. Platforms: `linux/amd64`, `linux/arm64`.
- Tags: `latest` (main branch), `X.Y.Z` and `X.Y` (releases), `sha-<commit>`.
- The image also contains `locklens` CLI commands (`inspect`, `watch`,
  `snapshot`, `doctor`) and the `locklens-lab` scenario runner used by the
  demo stack; the server never runs lab code.

## Safety

LockLens observes; it never kills sessions, cancels queries, changes
settings, or runs DDL/DML on monitored databases. Table browsing and the SQL
editor are off by default and use a separate SELECT-only role, inside
read-only transactions. Connection strings are encrypted and never shown,
logged, or exported.
