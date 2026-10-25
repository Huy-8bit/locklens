# LockLens frontend

The web UI of [LockLens](https://github.com/Huy-8bit/locklens), open-source
PostgreSQL monitoring and incident investigation: the fleet overview,
databases, topology, queries, sessions, blocking, the Explorer, the read-only
SQL editor, incidents, connections, users, and settings.

It is the built React app on nginx (unprivileged). It serves the UI and
proxies `/api/` to the
[backend image](https://hub.docker.com/r/johnnynguyen882/locklens-backend), so
the browser talks to a single origin (session cookie, CSRF, and CSP stay
strict).

## Quick start

See the [backend image](https://hub.docker.com/r/johnnynguyen882/locklens-backend)
for the complete `docker run` example (store, backend, frontend), or clone the
repository and run `make up`.

```sh
docker run -d --name locklens-frontend --network locklens -p 127.0.0.1:8080:8080 \
  -e LOCKLENS_BACKEND_URL=http://locklens-backend:8080 \
  johnnynguyen882/locklens-frontend:latest
```

Set `LOCKLENS_TRUSTED_PROXIES` on the backend to this container's name (or
address) so login rate limits apply per real client.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `LOCKLENS_BACKEND_URL` | `http://backend:8080` | Where `/api/` is proxied: scheme, host, and port of the backend, no path. It must resolve when the container starts. |

## Image details

- Port `8080`. `/` and every UI route serve the app (`index.html`,
  `Cache-Control: no-store`); `/assets/` are hashed and cached for a year;
  `/api/` goes to the backend, with the event stream unbuffered;
  `/frontend-healthz` is the container's health check.
- Sends `Content-Security-Policy` (`'self'` only), `X-Frame-Options: DENY`,
  `X-Content-Type-Options: nosniff`, and `Referrer-Policy: no-referrer`, and
  does not advertise its nginx version.
- Runs as nginx's unprivileged user. Platforms: `linux/amd64`, `linux/arm64`.
- Tags: `latest` (main branch), `X.Y.Z` and `X.Y` (releases). Use the same tag
  as the backend.
- Put TLS in front of it (a load balancer or another proxy) for anything
  beyond your own machine; then set `LOCKLENS_COOKIE_SECURE=true` on the
  backend.
