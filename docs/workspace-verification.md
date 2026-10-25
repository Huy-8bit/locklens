# Investigation workspace verification — 2026-10-24

## Environment and preservation

Working tree: branch `main`, existing HEAD `d0bf726`. No commits, pushes, resets or
stashes were performed. Existing uncommitted monitoring/chart work was retained.
The pre-existing unrelated `SECURITY.md` was compared byte-for-byte with the task
baseline and left unchanged. `local-lab/`, its `.env`, observations/screenshots,
node_modules, binaries and built/embedded assets remain ignored.

The local stack remains healthy at `http://127.0.0.1:8080`, with the fake CRUD API
at `http://127.0.0.1:8091`. The synthetic workload target has metadata/data/SQL enabled
with a dedicated read-only role; browser query history remains disabled. The Compose
primary and physical standby have an explicit provenance-bearing mapping. Fault modes
were disabled after testing; a bounded five-minute read workload was started for preview.

## Results

| Check | Actual result |
| --- | --- |
| `make check` | PASS: formatting, vet (including integration tags), dependency separation, unit tests and race detector |
| `make build` / backend binaries | PASS: React assets embedded under backend/internal/webui/dist; all three Go commands build |
| Frontend typecheck / lint | PASS |
| Frontend Vitest | PASS: 21 tests, 7 files |
| Storage + server integration, PostgreSQL 18.6 | PASS: migrations, rates/gaps, auth, target lifecycle, restricted role, statement aggregates, primary/standby, outage/recovery, metrics API |
| Workspace integration, PostgreSQL 14.24 and 18.6 | PASS: read-only rejection, multiple statements, DML CTE / row locks / COPY / ANALYZE rejection, null/empty strings, row/cell/protocol limits, parameterized filters, keyset pages, catalogs, EXPLAIN, timeout |
| Policy isolation, PostgreSQL 14.24 and 18.6 | PASS: default deny, metadata does not grant data, encrypted write-only DSN, concurrent limit, session ownership and active-query cancellation on policy revocation |
| Escaped JSON byte budget | PASS: repeated HTML-sensitive characters are counted after JSON escaping; serialized output stays bounded |
| Final catalog/duration changes, PostgreSQL 18.6 | PASS: catalog unknown estimates stay null; full statement duration includes declaration/planning |
| Docker smoke | PASS: readiness, migration 0003, embedded assets, SPA routes with per-document CSP nonces, CLI, lab runner, non-root runtime |
| Browser E2E, real isolated stack | PASS: 3 Chromium tests, approximately 1.3 minutes |
| Local relational browser verification | PASS: POST → visible item, PUT → changed value, DELETE → absent item, POST order → relational JOIN, SQL results, desktop/tablet overflow, no unexpected console errors |

Browser E2E covers login/Fleet/target, catalog → relation → data, filter/sort/keyset and
offset pages, SQL results and truncation, estimated EXPLAIN tree, rejected writes,
session-owned cancellation, disabled history/permissions, secret omission, real replication
node details, blocking incidents/export, synchronized zoom/pan/pause and responsive views.
The workspace test runs with the production CSP, without Playwright's CSP bypass.
The existing accessibility checks continue to pass. Browser coverage is Chromium only;
Firefox, Safari, IME composition and load testing beyond the stated limits were not run.
Vitest's jsdom prints a harmless canvas getContext availability warning; real chart
canvas rendering is checked by Chromium.

## Real workload observations

These are measurements from the synthetic local database, not UI fixtures:

- Write-heavy: 2,204 HTTP requests over 60 seconds, 36.73 HTTP requests/s, zero HTTP errors.
- Read-heavy: 1,416 HTTP requests over 45 seconds, 31.47 HTTP requests/s, zero HTTP errors.
- In the observed 15-minute metric window: tuple update rate peaked at approximately
  30.34 tuples/s; insert/delete rates at 1.87/1.80 tuples/s; WAL at 35,256 bytes/s;
  statement aggregate calls at 44.34/s. These are distinct measurements, not equivalent
  to HTTP throughput. Tuple reads include PostgreSQL/catalog work and are not SELECT counts.
- Blocking reached 8 waiters. Incidents #6 (idle transaction blocks sessions) and #7
  (persistent blocking) opened and resolved when the controlled fault ended.
  Timeout errors/dropped schedules during this deliberate blocking scenario are expected;
  they are not counted as successful requests.
- Replication topology uses observed primary/standby roles and the real streaming sender
  from the Compose deployment, corroborating an explicit mapping rather than name guessing.

Local observations are stored under ignored `local-lab/output/`.

## Screenshots reviewed

- `local-lab/output/screenshots/workspace-explorer.png`
- `local-lab/output/screenshots/workspace-sql.png`
- `local-lab/output/screenshots/workspace-sql-tablet.png`
- `local-lab/output/screenshots/verified/topology-desktop.png`
- `local-lab/output/screenshots/verified/desktop-tooltip.png`
- `local-lab/output/screenshots/verified/tablet.png`

Reviewed grid readability, SQL highlighting, details drawer, graph/viewport controls,
tablet layout, chart tooltips and overflow. Test screenshots use only synthetic data.

## Limits and unavailable features

- Host CPU/RAM/filesystem/disk measurements require node_exporter; it is not enabled in
  this local sandbox. PostgreSQL 14 lacks pg_stat_io. Missing sources are explicit.
- Statement-type and per-application throughput are unavailable from current sources;
  no classification or execution edges are fabricated.
- Target capabilities are per instance; roles (administrator, operator, viewer) decide who may use them.
- SQL is read-only and opt-in, not an absolute function sandbox or target resource governor.
- Catalog sections cap at 200 rows; table grids use 25 rows per page in the UI. General
  metadata search/pagination beyond that cap is not implemented. Catalog cache is browser-side.
- SQL results cap at 500 rows/100 columns, bounded serialized bytes and cells. Very large
  protocol values fail safely instead of returning a preview; exports contain only loaded rows.
- Keyset paging covers selected single-column integer/text/varchar/UUID primary keys.
  Composite/other sorts use bounded offset; no stable cross-request snapshot is promised.
- Fleet histories/findings show the first 12 targets; topology shows at most 40. A larger
  fleet requires navigation/pagination work, not silent background fan-out.
- Replication mappings are administrator-provided and corroborated by observations;
  automatic cryptographic cluster identity discovery is not implemented.
- Sessions and Activity sample open transactions and blocking participants, not every execution
  or every idle pooled session. Historical charts do not replay live activity/topology.
- No DML/DDL editor, EXPLAIN ANALYZE, backend termination, application tracing, automatic
  query-to-table graph, or persisted application-row history was added.
- The private `local-lab/` files are intentionally unversioned. The tracked demo and
  isolated browser test provision their own synthetic tables/roles for fresh clones.
