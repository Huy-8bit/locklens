# HTTP API

`locklens server` serves a JSON API under `/api/v1`, the web UI at `/`, and
operational endpoints. The UI uses only this API; anything it shows can be
fetched the same way.

The API is designed for the bundled UI on the same origin. It is versioned
(`/api/v1`), but field additions are not considered breaking.

## Conventions

- **Authentication**: a server-side session identified by the HttpOnly,
  `SameSite=Strict` cookie `locklens_session`, created by `POST
  /api/v1/auth/login`. There are no API tokens.
- **CSRF**: every request other than GET/HEAD/OPTIONS must send the session's
  CSRF token in `X-CSRF-Token` (returned by login and `GET /api/v1/session`).
  Requests with an `Origin` header for another host are rejected.
- **Errors**: `{"error": {"code": "...", "message": "...", "fields": {...},
  "request_id": "..."}}`. `fields` appears on validation errors (422).
  Internal errors return a generic message; details are only in the server log
  under the same request ID.
- **Request IDs**: every response carries `X-Request-ID` (a client-supplied
  value of up to 64 characters is reused).
- **Limits**: JSON bodies 1 MiB; incident import and snapshot upload 10 MiB;
  time ranges at most 31 days; `page_size` 1–200 (default 50).
- **Time ranges**: `range` as a Go duration (`15m`, `6h`, `168h`; days are
  not a unit), or `from`/`to` as RFC 3339 timestamps. `to` defaults to
  now. Each endpoint has its own default range.
- **Paging**: `page` (from 1) and `page_size`.
- **Unknown values**: counters that could not be computed (first sample,
  restart, stats reset, gap) are `null`, never `0`.
- **Credentials and query text**: DSNs and passwords never appear in
  responses. Query text appears only when the target opted in **and** the
  request asks for it (`include_text=true`, or the session sees it in live
  activity of an opted-in target).

## Operational endpoints (no authentication)

| Method and path | Description |
| --- | --- |
| `GET /healthz` | Liveness: `200 {"status":"ok"}` while the process serves HTTP. |
| `GET /readyz` | Readiness: `200` when the monitoring store answers and its schema version is readable, else `503` with a reason. |
| `GET /metrics` | Prometheus text format (when `LOCKLENS_METRICS_ENABLED`). Labels are bounded: collector names, outcomes, target IDs, route classes. No query text, DSNs, or user input. |

Series: `locklens_collect_duration_seconds{collector}`,
`locklens_collect_total{collector,outcome}`,
`locklens_collect_last_success_timestamp_seconds{target}`,
`locklens_collect_slots_busy`, `locklens_active_targets`,
`locklens_write_queue_length`, `locklens_write_queue_capacity`,
`locklens_store_write_errors_total`, `locklens_store_dropped_batches_total`,
`locklens_incidents_opened_total`, `locklens_incidents_open`,
`locklens_http_requests_total{route,code}`, `locklens_sse_clients`.

## Authentication

| Method and path | Description |
| --- | --- |
| `POST /api/v1/auth/login` | Body `{"username","password"}`. `200` returns `{"username","csrf_token","expires_at","server":{...}}` and sets the cookie. `401` on bad credentials, `429` with `Retry-After` when rate-limited (5 failures per client per minute). |
| `POST /api/v1/auth/logout` | Ends the session. |
| `GET /api/v1/session` | Current session (same shape as login). `401` when signed out. |

## Targets

| Method and path | Description |
| --- | --- |
| `GET /api/v1/fleet` | All targets with status, latest key metrics, and counts of open incidents and current findings. |
| `GET /api/v1/targets` | Targets with status. |
| `POST /api/v1/targets` | Create. Body: `name`, `description`, `dsn`, `enabled`, `interval_seconds`, `max_connections`, `query_text_enabled`, `node_exporter_url`. `201` with the target; `422` with `fields`; `409` on a duplicate name. |
| `POST /api/v1/targets/test` | Test a DSN without saving it. Body `{"dsn"}`. |
| `GET /api/v1/targets/{id}` | One target. |
| `PUT /api/v1/targets/{id}` | Update (same body as create; an empty `dsn` keeps the stored one). Switching `query_text_enabled` off deletes the target's stored statement texts. |
| `DELETE /api/v1/targets/{id}` | Soft delete (history kept until retention). `?purge=true` deletes its history now; with `&keep_incidents=false` its incidents are deleted too (kept by default). |
| `POST /api/v1/targets/{id}/test` | Test the stored connection: `{"ok","duration_ms","target","capabilities","checks":[...],"error","hint"}`. |

A target's `status` holds `state` (`pending`, `healthy`, `degraded`,
`unreachable`, `disabled`; see [collectors.md](collectors.md#target-states)),
`server_version`, `is_standby`, `capabilities`, per-collector status
(`enabled`, `disabled_reason`, `last_success`, `last_error`,
`last_duration_ms`, `consecutive_failures`), `last_error`, and
`freshness_seconds`.

## Target data

| Method and path | Default range | Description |
| --- | --- | --- |
| `GET /api/v1/targets/{id}/overview` | | Latest values, session counts, blocking summary, visibility warnings, current findings, open incidents, host status. |
| `GET /api/v1/targets/{id}/metrics?names=a,b` | 1h | Time series for 1–20 metric names (see [collectors.md](collectors.md#metrics)). Downsampled to at most 400 buckets per series; each point has `t`, `v` (null when unknown), `n` samples, and `known` samples. Includes collection `gaps` in the range. |
| `GET /api/v1/targets/{id}/activity` | | Latest activity report (sessions, blocking graph, long transactions, vacuum progress): live from memory, else the last stored snapshot (`source`: `live`, `stored`, `none`). |
| `GET /api/v1/targets/{id}/blocking/snapshots` | 24h | Stored activity snapshots (`?blocking=true` for those with blocking; `limit` ≤ 1000). |
| `GET /api/v1/targets/{id}/blocking/snapshots/{sid}` | | One stored snapshot rendered as a report. |
| `GET /api/v1/targets/{id}/queries` | 1h | Workload from pg_stat_statements; see below. |
| `GET /api/v1/targets/{id}/queries/{identity_id}` | 1h | One query identity: buckets over time, current vs. previous equal window, comparison, coverage. |
| `GET /api/v1/targets/{id}/tables` | | Table/vacuum health, vacuum progress, long transactions (live or last stored). |
| `GET /api/v1/targets/{id}/replication` | | Role, replicas with LSN lags and time-lag fields, slots with retained WAL, standby receiver state, archiver. |
| `GET /api/v1/targets/{id}/host` | | node_exporter metrics: `configured`, `available`, `host`, `error`. |
| `GET /api/v1/targets/{id}/findings` | | Findings; `?status=current` or `resolved`. |
| `GET /api/v1/targets/{id}/gaps` | 24h | Collection gaps (`collector`, `from`, `to`, `reason`). |

### Query workload parameters

`GET /api/v1/targets/{id}/queries` accepts:

| Parameter | Description |
| --- | --- |
| `range`, `from`, `to` | Window. |
| `sort` | `total_time` (default), `calls`, `mean_time`, `rows`, `shared_read`, `temp_written`, `wal_bytes`, and with `compare=true` also `mean_ratio`, `calls_ratio`, `total_ratio`. |
| `order` | `desc` (default) or `asc`. |
| `compare=true` | Adds the previous window of equal length and a per-query comparison with flags. |
| `q` | Filter on database, user, query ID, and (when included) text; ≤ 200 characters. |
| `include_text=true` | Include text if the target opted in. |
| `include_monitoring=true` | Include statements run by the target's monitoring user (hidden by default). |

The response includes `coverage` (seconds of the window actually covered by
samples, number of collections, resets, evicted entries), `available` and
`unavailable_reason`, and `min_calls` used by the comparison flags. Query IDs
are strings (64-bit values do not fit JavaScript numbers). See
[query-analysis.md](query-analysis.md).

## Incidents

| Method and path | Description |
| --- | --- |
| `GET /api/v1/incidents` | List, newest first. Filters: `target`, `state` (`open`/`resolved`), `rule`, `range`/`from`/`to`; paging. |
| `GET /api/v1/incidents/{id}` | Incident and linked findings. |
| `GET /api/v1/incidents/{id}/events` | Evidence timeline; `after` (event ID) and `limit` for incremental loading. |
| `POST /api/v1/incidents/{id}/acknowledge` | Body `{"note"}` (optional). Adds metadata only; evidence is never changed. |
| `GET /api/v1/incidents/{id}/export?format=json\|html` | Download. Query text is removed unless `include_query=true` (and only text that was collected under opt-in exists at all). |
| `POST /api/v1/incidents/import` | Upload an exported JSON file (≤ 10 MiB, validated); stored as a read-only incident with `source: import`. |

See [incidents.md](incidents.md) for the format.

## Other

| Method and path | Description |
| --- | --- |
| `POST /api/v1/snapshots/analyze` | Upload a CLI snapshot file (`locklens snapshot save`, ≤ 10 MiB) and get the rendered report. Nothing is stored. |
| `GET /api/v1/settings/rules` | Current rule thresholds, defaults, and descriptions. |
| `PUT /api/v1/settings/rules` | Replace thresholds (validated ranges). |
| `GET /api/v1/storage` | Monitoring store table sizes and row estimates. |
| `GET /api/v1/events` | Server-sent events: `target.collected`, `target.status`, `incident.updated` with `{"target_id"}` or `{"incident_id"}` payloads. The UI uses them to refresh; they carry no data themselves. |

## Example

```sh
base=http://127.0.0.1:8080
csrf=$(curl -s -c jar -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"…"}' $base/api/v1/auth/login | jq -r .csrf_token)
curl -s -b jar "$base/api/v1/fleet" | jq '.targets[].target | {name, state: .status.state}'
curl -s -b jar -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' \
  -d '{"name":"orders","dsn":"postgres://observer@db:5432/orders?sslmode=verify-full"}' \
  $base/api/v1/targets
```
