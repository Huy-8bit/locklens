# Deployment

LockLens runs as two images plus a PostgreSQL database for its own data:

| Component | Image | Contents | Listens |
| --- | --- | --- | --- |
| Backend | `locklens-backend` ([backend/Dockerfile](../backend/Dockerfile)) | `locklens server`: collectors, rules, incident recorder, REST API and live events; the `locklens` CLI; the `locklens-lab` scenario runner | `8080`: `/api/`, `/healthz`, `/readyz`, `/metrics` |
| Frontend | `locklens-frontend` ([web/Dockerfile](../web/Dockerfile)) | The built React UI on nginx (unprivileged), with a reverse proxy for `/api/` | `8080`: the UI, `/api/` (proxied), `/frontend-healthz` |
| Store | any PostgreSQL 14+; TimescaleDB recommended (`timescale/timescaledb:2.30.2-pg18` in Compose) | LockLens' history, users, encrypted connection strings | `5432` |

```mermaid
flowchart LR
    B[Browser] -- "UI and /api/ (one origin)" --> F[frontend<br/>nginx :8080]
    F -- "/api/ (proxy)" --> A[backend<br/>locklens server :8080]
    A --> S[(store<br/>PostgreSQL + TimescaleDB)]
    A -- "read-only" --> T[(monitored<br/>PostgreSQL)]
```

The browser only talks to the frontend. Because the UI and the API share one
origin, the session cookie stays `HttpOnly` and `SameSite=Strict`, CSRF checks
compare the `Origin` with the `Host` the frontend passes through, and the
Content Security Policy allows only `'self'`. Expose the backend to the
frontend (and to Prometheus, for `/metrics`), not to browsers.

## Docker Compose

[deploy/compose.yaml](../deploy/compose.yaml) runs the three components:

```sh
make up      # creates deploy/.env with random secrets on the first run, then starts store, backend, frontend
make down    # stops them; data is kept in the locklens_store volume
```

Only the frontend is published, on `127.0.0.1:${LOCKLENS_HTTP_PORT:-8080}`.
Secrets come from `deploy/.env` (see [deploy/.env.example](../deploy/.env.example)).
The demo lab is a separate overlay, [deploy/compose.lab.yaml](../deploy/compose.lab.yaml)
(`make lab-up`, see the [quickstart](quickstart.md)).

## docker run

```sh
export LOCKLENS_MASTER_KEY="$(openssl rand -base64 32)"   # keep it

docker network create locklens
docker run -d --name locklens-store --network locklens \
  -e POSTGRES_USER=locklens -e POSTGRES_PASSWORD=change-me -e POSTGRES_DB=locklens \
  -v locklens-store:/var/lib/postgresql timescale/timescaledb:2.30.2-pg18 \
  postgres -c shared_preload_libraries=timescaledb -c timescaledb.telemetry_level=off
docker run -d --name locklens-backend --network locklens \
  -e LOCKLENS_STORE_DSN='postgres://locklens:change-me@locklens-store:5432/locklens?sslmode=disable' \
  -e LOCKLENS_MASTER_KEY -e LOCKLENS_ADMIN_PASSWORD='choose-a-long-password' \
  -e LOCKLENS_COOKIE_SECURE=false -e LOCKLENS_TRUSTED_PROXIES=locklens-frontend \
  johnnynguyen882/locklens-backend:latest
docker run -d --name locklens-frontend --network locklens -p 127.0.0.1:8080:8080 \
  -e LOCKLENS_BACKEND_URL=http://locklens-backend:8080 \
  johnnynguyen882/locklens-frontend:latest
```

Start the frontend after the backend: nginx resolves `LOCKLENS_BACKEND_URL`
when it starts.

## Settings that connect the two

| Where | Variable | Value |
| --- | --- | --- |
| Frontend | `LOCKLENS_BACKEND_URL` | `scheme://host:port` of the backend, no path (default `http://backend:8080`) |
| Backend | `LOCKLENS_TRUSTED_PROXIES` | The frontend's host name, IP, or CIDR (comma-separated). The backend then takes the client address for login rate limits and logs from `X-Forwarded-For`, using the right-most address that is not a trusted proxy. Host names are resolved every 30 seconds. Unset: the connecting address is used, so every login through the frontend shares one rate limit. Do not list networks that browsers connect from: they could choose their address. |
| Backend | `LOCKLENS_COOKIE_SECURE` | `true` once the frontend is reached over HTTPS; `false` only for plain HTTP on your own machine |

Everything else is backend configuration ([configuration.md](configuration.md)).

## TLS and other proxies

Terminate TLS in front of the frontend (a load balancer, an ingress, or
another reverse proxy), then set `LOCKLENS_COOKIE_SECURE=true` on the backend.
Such a proxy must:

- keep the `Host` header (the backend's CSRF check compares it with `Origin`);
- not buffer `/api/v1/events` (server-sent events);
- allow request bodies of at least 11 MiB (incident import);
- be added to `LOCKLENS_TRUSTED_PROXIES` too if it appends to
  `X-Forwarded-For` and you want per-client login limits.

You can also skip the frontend image and serve `web/dist` (`make web`) with
any web server that proxies `/api/` to the backend with the same rules; copy
the headers from [web/nginx/default.conf.template](../web/nginx/default.conf.template).

## Several servers

For availability, run two or more backends with the same
`LOCKLENS_STORE_DSN` and `LOCKLENS_MASTER_KEY` behind the frontend (or any
load balancer; sticky sessions keep SQL workspace cancellation on the server
that runs the statement). They split the instances by leases and take over
each other's within about a minute; see
[architecture.md](architecture.md#several-servers). Give each a distinct
`LOCKLENS_SERVER_ID` if you want stable names in Settings → Data and storage.
The monitoring store itself is a single PostgreSQL; make it highly available
with your usual PostgreSQL tooling.

## Log agent

To ship server logs, create a token of scope "Ship logs" for the instance
(Settings → API tokens) and run the agent on the database host, where it can
read the log directory (for example as the `postgres` user or a member of its
group):

```bash
LOCKLENS_AGENT_TOKEN=llk_... locklens agent --server https://locklens.example.com \
  --log /var/lib/postgresql/data/log --state-dir /var/lib/locklens-agent
```

The same image runs it: `docker run -v /var/lib/postgresql/data/log:/log:ro
-v locklens-agent:/state -e LOCKLENS_AGENT_TOKEN … <backend image> agent
--server … --log /log --state-dir /state`. See
[configuration.md](configuration.md#log-agent) for the logging settings it
needs.

## Versions and upgrades

Run the same tag of both images (`X.Y.Z` for releases, `latest` for the main
branch). The backend applies store migrations at startup under an advisory
lock; back up the store before upgrading across releases
([storage-and-retention.md](storage-and-retention.md)). Images are built for
`linux/amd64` and `linux/arm64` and run as non-root users.

## Health checks

| Check | Meaning |
| --- | --- |
| Backend `GET /healthz` | The process serves HTTP |
| Backend `GET /readyz` | The store answers and its schema is readable |
| Frontend `GET /frontend-healthz` | nginx serves requests (it does not check the backend) |
