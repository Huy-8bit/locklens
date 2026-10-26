# LockLens backend

The API server of [LockLens](https://github.com/Huy-8bit/locklens), open-source
PostgreSQL monitoring and incident investigation: collectors, rules, the
incident recorder, the REST API with live events, and the `locklens` CLI.

It serves no web UI. Run it with the
[frontend image](https://hub.docker.com/r/johnnynguyen882/locklens-frontend),
which serves the UI and proxies `/api/` to this backend, so the browser talks
to a single origin.

## Quick start

LockLens keeps its history in its own PostgreSQL database (the *store*), never
in the databases it monitors.

```sh
# Generate the master key once and keep it (a password manager, a secret store).
export LOCKLENS_MASTER_KEY="$(openssl rand -base64 32)"

docker network create locklens

docker run -d --name locklens-store --network locklens \
  -e POSTGRES_USER=locklens -e POSTGRES_PASSWORD=change-me -e POSTGRES_DB=locklens \
  -v locklens-store:/var/lib/postgresql timescale/timescaledb:2.30.2-pg18 \
  postgres -c shared_preload_libraries=timescaledb -c timescaledb.telemetry_level=off

docker run -d --name locklens-backend --network locklens \
  -e LOCKLENS_STORE_DSN='postgres://locklens:change-me@locklens-store:5432/locklens?sslmode=disable' \
  -e LOCKLENS_MASTER_KEY \
  -e LOCKLENS_ADMIN_PASSWORD='choose-a-long-password' \
  -e LOCKLENS_COOKIE_SECURE=false \
  -e LOCKLENS_TRUSTED_PROXIES=locklens-frontend \
  johnnynguyen882/locklens-backend:latest

docker run -d --name locklens-frontend --network locklens -p 127.0.0.1:8080:8080 \
  -e LOCKLENS_BACKEND_URL=http://locklens-backend:8080 \
  johnnynguyen882/locklens-frontend:latest
```

Open http://127.0.0.1:8080, sign in as `admin`, and add a database under
**Settings → Add instance** by pasting its connection string. Keep the master
key: it encrypts stored connection strings. `LOCKLENS_COOKIE_SECURE=false` is
only for plain HTTP on your own machine; behind HTTPS, leave it unset.

## Configuration

| Variable | Required | Description |
| --- | --- | --- |
| `LOCKLENS_STORE_DSN` | yes | PostgreSQL 14+ database for LockLens' own data |
| `LOCKLENS_MASTER_KEY` | yes | 32 random bytes, base64 (`openssl rand -base64 32`) |
| `LOCKLENS_ADMIN_PASSWORD` or `LOCKLENS_ADMIN_PASSWORD_HASH` | first start | The environment administrator (12–72 characters, or a bcrypt hash); add more users with roles in the UI |
| `LOCKLENS_TRUSTED_PROXIES` | with the frontend | IPs, CIDRs, or host names of the frontend, whose `X-Forwarded-For` is believed for login rate limits |
| `LOCKLENS_LISTEN` | no | Default `0.0.0.0:8080` in this image |
| `LOCKLENS_RETENTION` | no | History retention, default `168h` |
| `LOCKLENS_LOG_FORMAT` | no | `text` (default) or `json` |

Full list: [configuration](https://github.com/Huy-8bit/locklens/blob/main/docs/configuration.md).

## Image details

- Port `8080`: `/api/` (REST and server-sent events), `GET /healthz`
  (liveness), `GET /readyz` (store reachable), `/metrics` (Prometheus). Expose
  it to the frontend only, not to browsers directly.
- Runs as the non-root user `10001`. Platforms: `linux/amd64`, `linux/arm64`.
- Tags: `latest` (main branch), `X.Y.Z` and `X.Y` (releases). Use the same tag
  for the backend and the frontend.
- Also contains the CLI (`locklens inspect`, `watch`, `snapshot`, `doctor`) and
  the `locklens-lab` scenario runner used by the demo lab.

## Safety

LockLens observes; it never kills sessions, cancels queries, changes
settings, or runs DDL/DML on monitored databases. The only exception is
creating its own least-privilege roles when an administrator pastes a
superuser connection string and confirms the shown statements; the superuser
connection string is never stored. Connection strings are encrypted and never
shown, logged, or exported.
