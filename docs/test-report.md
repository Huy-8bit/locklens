# Test report

What was run, on what, and with which result. Test design:
[testing.md](testing.md). Nothing below is marked PASS that was not run.

- **Execution date:** 2026-10-24 (phase 2), 2026-10-25 (UI rebuild and mockup UI, first sections)
- **Code under test:** working tree on top of commit `db0d42a` (phase 2 not
  yet committed)
- **Scope:** phase 2 (monitoring server, UI, lab) in the first part; the
  earlier phase 1 CLI QA run is kept as an appendix.

## Separate backend and frontend images (2026-10-25)

The single image (Go server with the UI embedded) was split into
`locklens-backend` (`backend/Dockerfile`: API, collectors, CLI, lab runner; no
UI) and `locklens-frontend` (`web/Dockerfile`: the built UI on unprivileged
nginx, proxying `/api/` to the backend). The backend gained
`LOCKLENS_TRUSTED_PROXIES` (IPs, CIDRs, or host names) so login rate limits
use the real client address behind the frontend. Compose runs store, backend,
and frontend; only the frontend is published.

| Check | Result |
| --- | --- |
| `make check` and `make web-check` (new: client address behind trusted proxies, incl. host names and spoofing attempts; trusted proxy configuration) | PASS |
| `make docker-smoke` (rewritten for both images: backend readiness and no UI, frontend UI/assets/SPA routing/headers, `/api/` proxy with cookie and CSRF origin check, CLI, lab runner, non-root users) | PASS |
| Browser E2E through the frontend's nginx (`make e2e`, no API mocks) | PASS (1 test, 1.0 min) |
| Integration server suite, PostgreSQL 18.6 | PASS |
| Server-sent events through nginx | PASS (observed: 13 events in 12 s, unbuffered) |
| Login log behind the frontend shows the client address (Docker gateway), not the frontend container | PASS (observed) |
| The documented `docker run` commands (store, backend, frontend) | PASS (observed: UI 200, login 200) |
| Both images built for `linux/amd64` on an arm64 machine; run as non-root | PASS (backend 32.5 MB, frontend 50.8 MB) |

Found and fixed: security headers set at the nginx server level would have
been added on top of the backend's own headers on `/api/` responses (now set
per static location); a default that trusted all private networks as proxies
would have let clients on those networks choose their rate-limit key (the
default is now the frontend's host name); `make down` left the container of
the former `locklens` service running and holding the port (now
`--remove-orphans`).

Not run for this change: the 14.24 integration run and the other packages of
the integration suite (they do not involve the HTTP layer), GitHub Actions.


Users with roles (administrator, operator, viewer) and adding an instance from
a single connection string, including automatic least-privilege roles for a
superuser connection string.

| Check | Result |
| --- | --- |
| `make check` (gofmt, vet incl. integration tags, separation, unit, race), incl. the role × route matrix (20 routes × 3 roles), auth, and DSN rewriting tests | PASS |
| Frontend typecheck, ESLint, Vitest (17 tests; new: viewer has no SQL editor, users management, superuser quick add with confirmation) | PASS |
| Integration harness, PostgreSQL 14.24 and 18.6, now including `internal/domain/onboard` (create roles on the primary, reuse on the standby, reader cannot `pg_terminate_backend` or `pg_read_file`, direct mode, wrong password not echoed) | PASS (7 packages per version) |
| Browser E2E (`make e2e`, no API mocks), new steps: superuser onboarding in the UI, viewer signs in and is refused SQL and users | PASS (1 test, 15 steps, 1.1 min) |
| axe-core with color contrast on Account, Settings (users), Add instance, advanced form, Explorer, SQL editor, light and dark | PASS after fixes |
| Live check against the external Try_Hard lab (PostgreSQL 16.15 primary and streaming replica, 3.2 M rows): superuser DSN → roles created after confirmation (attributes verified in `pg_roles`), replica added by reusing them, topology, read-only pgbench load, a row-lock chain on `orders`, Explorer and SQL editor as operator, viewer refused | PASS (observed) |

Found and fixed while verifying:

- store lookups of users returned a wrapped "not found", so a comparison in the
  storage test failed (now returned unwrapped like other lookups);
- the workspace route checked service availability before the permission
  (the permission is now checked first);
- inline links distinguishable only by color, and keyboard-unreachable
  scrolling statement blocks (axe);
- the E2E and Docker smoke scripts did not randomize the ports of the
  `standby-2` and `backend-simulator` services added to the lab stack, so they
  collided with a running lab;
- an E2E step picked the first UPDATE statement, which can now come from the
  stack's background workload.

Observed but not fixed: in 2 of about 8 E2E runs one of the two lab standbys
exited during startup (both clone the primary at the same time); a rerun
passed. Its log was not captured. A check for the superuser password in the
server log matched only because the lab password `postgres` is also part of
the host name `postgres-primary`; the full connection string never appeared in
logs or in the store.

Not run: integration on 15.19, 16.15, and 17.11, the performance smoke check,
and GitHub Actions.


The UI was reworked to the reference mockup: fleet Overview (six KPIs,
read/write operations, throughput and latency, system resources, topology,
top queries, active sessions, blocking, explorer, SQL editor), Databases with
per-instance tabs, Topology, Queries across instances, Sessions, Blocking,
Explorer, read-only SQL Editor, Incidents, Connections, Settings. The backend
gained the target environment (migration `0005`), fleet and system endpoints,
connection groups, and catalog functions, types, and triggers.

| Check | Result |
| --- | --- |
| `make check` (gofmt, vet incl. integration tags, separation, unit, race), incl. new fleet aggregation, auth, environment, and counter-total tests | PASS |
| Frontend typecheck, ESLint, Vitest (14 tests incl. axe; new: fleet Overview without invented values, SQL editor run and plan) | PASS |
| Frontend production build | PASS (app 352 KB, charts chunk 532 KB before gzip) |
| Live lab (PostgreSQL 18.6, fresh volumes): migration 5, environment `lab`, node_exporter, demo view/function/type/trigger, `shop`, `chain`, and `idle-blocker` scenarios on every page | PASS (observed) |
| SQL editor against the lab: a join returns rows, EXPLAIN draws Limit → Sort → Hash Join → Seq Scan, an UPDATE is refused by PostgreSQL | PASS (observed, light and dark) |
| Screenshots of 17 pages at 1600 px light and dark, and 390 px light and dark | PASS: no console errors, no failed requests, no horizontal overflow (after fixes) |
| axe-core with color contrast, 17 pages + login, light and dark, live lab | PASS after fixes |
| Integration harness, PostgreSQL 14.24 and 18.6 | PASS (6 packages per version) |
| Browser E2E rewritten for the new navigation (`make e2e`, no API mocks) | PASS (1 test, 14 steps, 58.6 s) |

Found and fixed while verifying:

- KPI labels and subtitles were truncated at 1600 px (now wrap);
- standby cards were squeezed to one character per line in the Overview
  topology card (stacked layout in the card);
- the throughput and latency panes did not share x positions (fixed y-axis
  width);
- the Top queries and Active sessions cards overflowed their column (fewer
  columns in the compact cards);
- full-width selects in widget toolbars;
- CPU showed "—" right after a restart while the history had values (falls
  back to the last known bucket);
- the account menu's accessible name did not contain its visible text;
- E2E selectors that matched two elements (trigger name and definition; the
  standby listed under two targets of the same server).

Not run for the mockup UI: the 15.19, 16.15, and 17.11 integration runs, the
performance smoke check, and GitHub Actions (nothing pushed). The lab's
node_exporter runs in a container, so its CPU, memory, and filesystem values
describe the Docker VM, not a database host.

## UI rebuild (2026-10-25)

The web UI was deleted and rebuilt around "what is this database doing now":
Overview, Live activity, Queries (by statement type), Data explorer,
Replication, Alerts, All instances, Settings. The backend gained statement
command classification (migration `0004`), `GET /queries/commands`,
SQL text collection on by default, and lab bootstrap of the data explorer
(separate `locklens_reader` role, demo `shop` schema, `shop` scenario).

| Check | Result |
| --- | --- |
| `make check` (gofmt, vet incl. integration tags, separation, unit, race) | PASS |
| Frontend typecheck, ESLint, Vitest (12 tests incl. axe on 4 pages) | PASS |
| Frontend production build | PASS (pages lazy-loaded; app 333 KB, charts chunk 510 KB before gzip) |
| Classifier unit tests (30 statements, malformed input) | PASS |
| Live lab (PostgreSQL 18.6, fresh volumes): migrations 1–4, bootstrap of data access for both lab targets, `shop` traffic classified as select/insert/update/delete/transaction/ddl/maintenance via the API | PASS (observed) |
| Screenshots of 14 pages, desktop light and dark | PASS: no console errors, no failed requests, no horizontal overflow |
| Screenshots at 390 px, light and dark | PASS after fixes (14 pages): no console errors, failed requests, or overflow |
| axe-core with color contrast, 12 pages + login, light and dark, live lab | PASS after fixes |
| Integration harness, PostgreSQL 14.24 and 18.6 (CLI, postgres, storage, workspace, incidents, server suites; server tests updated for SQL text by default and the command breakdown) | PASS (6 packages per version) |
| Browser E2E rewritten for the new UI (`make e2e`, no API mocks) | PASS (1 test, 12 steps, 47 s) |

Found and fixed during the rebuild:

- rates by type treated quiet buckets as unknown (the backend now returns
  per-bucket coverage, so a covered bucket without statements is zero);
- the explorer issued more concurrent workspace requests than the server
  allows, producing 429s (client-side queue of two per target, retry on 429);
- horizontal overflow at 390 px: the breakdown table, and a visually hidden
  table-header label positioned against the page instead of its table;
- color contrast below 4.5:1: white on the accent button, the light muted text
  on the page background, and the dark muted text on a selected row (new
  `--accent`, `--accent-fill`, and muted values, measured);
- an invalid `<dl>` structure on All instances;
- E2E selectors that matched the hidden instance `<option>`, and contrast
  measured during the 150 ms theme transition.

The local Docker engine (OrbStack) stopped responding for a while during an
OrbStack self-update; the checks above were run after it recovered.

Not run for the rebuild: the 15.19, 16.15, and 17.11 integration runs (the
earlier phase 2 matrix passed on all five versions before these changes), the
performance smoke check, and GitHub Actions (nothing pushed).

## Phase 2 summary

| Area | Command | Result |
| --- | --- | --- |
| Go formatting, vet, import separation, unit tests, race detector | `make check` | PASS |
| Frontend typecheck, ESLint, Vitest (17 tests, axe-core) | `make web-check` | PASS |
| Frontend production build, embedded UI, binaries | `make build-all` | PASS (bundle 801 KB, 233 KB gzip) |
| Integration: CLI, collector, store, recorder, server suites | `scripts/integration-test.sh` | PASS on PostgreSQL 14.24, 15.19, 16.15, 17.11, 18.6 (see matrix) |
| Browser E2E against the Docker stack, no API mocks | `make e2e` | PASS (1 test, 13 steps, 36.7 s on the final tree) |
| Docker lab, nine scenarios | `make up`, `make lab SCENARIO=...`, `make lab-outage` | Each scenario produced the expected incident or finding (see below) |
| UI review in a real browser | Playwright screenshots, 13 pages × light, dark, 390 px light, 390 px dark | No console errors, no horizontal overflow after fixes |
| Performance smoke check | `scripts/perf-smoke.sh` (20 targets, 5 min) | See measurements |

## Environment

| Item | Value |
| --- | --- |
| Host | macOS 15.7.4, Apple M1 Pro (8 cores), 32 GiB RAM, arm64 |
| Go | go1.26.0 darwin/arm64 |
| Node.js / npm | 24.7.0 / 11.5.1 (Docker image build: `node:24-alpine`) |
| Playwright | 1.64.0, Chromium |
| Docker | Engine 29.8.2 via OrbStack, Docker Compose 5.5.0 |
| PostgreSQL | official `postgres:<version>-alpine` images 14.24, 15.19, 16.15, 17.11, 18.6 (digests in the appendix); lab and store use 18.6 |

## Integration matrix

`scripts/integration-test.sh` with `LOCKLENS_IT_PG_VERSIONS="14.24 15.19
16.15 17.11 18.6"`, race detector on, one isolated Compose project per version
(primary with `pg_stat_statements` preloaded plus a streaming standby).

| Package | Tests | 14.24 | 15.19 | 16.15 | 17.11 | 18.6 |
| --- | --- | --- | --- | --- | --- | --- |
| `tests/e2e/cli` (CLI end to end) | 34 | PASS | PASS | PASS | PASS | PASS |
| `backend/internal/lib/postgres` (tag `integration`) | 1 | PASS | PASS | PASS | PASS | PASS |
| `backend/internal/repository` (tag `integration`) | 6 | PASS | PASS | PASS | PASS | PASS |
| `backend/internal/domain/incidents` (tag `integration`) | 5 | PASS | PASS | PASS | PASS | PASS |
| `tests/e2e/server` (server over HTTP) | 6 | PASS | PASS | PASS | PASS | PASS |

In the first matrix run, `tests/e2e/server` failed to *build* on 14.24 and 15.19
because a `make web` run in parallel replaced the embedded UI files during
compilation (`embed ... no such file or directory`). That was interference
from the test operator, not a product defect; both versions were re-run
afterwards with nothing else touching the tree (results above).

## Browser end-to-end

`./scripts/e2e.sh`: isolated `deploy/compose.yaml` project on random loopback
ports, Playwright Chromium, one test with 13 steps (sign-in, add and test a
target, live data, lab `chain` scenario, incident evidence, HTML export,
JSON export and re-import, offline report with JavaScript disabled and the
network offline, remaining pages, theme switch, axe-core on every page,
sign-out, no console errors or failed requests). Final run on the final tree: **PASS** (36.7 s), stack removed afterwards.

Earlier E2E runs found issues that were fixed (defects 3, 4, 5, and 8 below).

## Lab scenarios

Run against the Docker lab (PostgreSQL 18.6 primary and standby, demo mode),
observed through the API and UI:

| Scenario | Observed |
| --- | --- |
| `healthy` (2 min) | Activity recorded (up to 22 commits/s), no current findings on either target, no open incidents |
| `row-blocking` | `blocking.long_chain` incident; resolved ("condition cleared") after release |
| `chain` | Blocking tree A → B → C (2 blocked in total); incident with pre-trigger samples and stored graphs |
| `idle-blocker` | Critical `blocking.idle_in_tx_blocker` and `blocking.long_chain` incidents; `transactions.long` findings after 60 s |
| `connection-pressure` | `connections.pressure` incident at 91% of available slots; resolved after the scenario |
| `regression` | `queries.mean_regression` incident: same query 2.8× slower per call after the index was dropped; `queries.call_volume_increase` info finding |
| `temp-files` | `temp_files.increase` finding (626 MiB per 2-minute window) |
| `replication` | `replication.slot_retained_wal` incident (198 MiB retained by `lab_stale_slot`), `replication.replica_lag` findings on the primary and on the standby; all resolved after the scenario restored replay and dropped the slot |
| `outage` (`make lab-outage`, also `TestOutageAndRecovery`) | Target `unreachable` during the outage, healthy afterwards, collection gap recorded |

## UI review

Screenshots of Fleet, Overview, Queries, Blocking, Sessions, Tables,
Replication (primary and standby), Incidents, Incident detail, Settings,
target form, and Lab guide in light and dark themes at 1440 px and 390 px,
with real lab data during an `idle-blocker` scenario. The checker fails on
console errors or warnings and on horizontal overflow. Issues found and fixed:
grid columns overflowing at 390 px, fractional axis ticks on count charts,
"unknown / unknown" truncated in tiles (defects 9–11). The final pass reported
none. Three screenshots are in [images/](images/).

## Performance measurements

`scripts/perf-smoke.sh` with 20 targets (10 on the lab primary, 10 on the
standby), 10 s interval, production collector intervals, concurrency limit 4,
300 s, no scenario running:

| Collector | Runs | Mean | p95 (histogram bucket) | Errors |
| --- | --- | --- | --- | --- |
| activity | 620 | 8.7 ms | ≤ 25 ms | 0 |
| database | 420 | 5.9 ms | ≤ 25 ms | 0 |
| statements | 120 | 5.6 ms | ≤ 10 ms | 0 |
| replication | 220 | 4.4 ms | ≤ 10 ms | 0 |
| tables | 40 | 12.1 ms | ≤ 25 ms | 0 |

- Concurrent collector runs: max 4 (limit 4); write queue max 14 batches, no
  drops, no store write errors.
- Connections held by the server: 10 to the primary, 10 to the standby (one per
  target, as configured), 8 to the store.
- Resident memory 32.8 MiB; 3.1 s CPU time over 5 minutes.
- Store growth 4.66 MiB in 5 minutes, about 2.8 MiB per target-hour.

A longer demo-mode run (2 targets, 36 minutes, scenarios running) grew the
store by about 7 MB ([storage-and-retention.md](storage-and-retention.md#storage-growth)).

Limits: one laptop, one short run, small lab databases with few sessions and
statements, loopback networking. These are smoke checks, not benchmarks or
capacity guarantees.

## Defects found and fixed in phase 2 verification

| # | Defect | Fix | Regression test |
| --- | --- | --- | --- |
| 1 | An open incident was resolved as "condition cleared" while the condition persisted, when the root blocker changed or after a LockLens restart, because persistence-based rules need several samples before firing again; it was then reopened within the cooldown | "Clear" now means the trigger condition is absent from the current sample; insufficient data is not clear | `TestRootChangeDoesNotResolve`, `TestRestartRecovery` (both fail without the fix) |
| 2 | A target's collector list lacked collectors that had not run yet (for example `statements` right after connecting), so the UI could not show why they were unavailable | Every collector's status is published when the worker starts and after capability detection | `TestTargetLifecycleBlockingIncidentAndExport` |
| 3 | The new-target form requested `GET /api/v1/targets/0` (400 in the console) | The target query is not run for id 0 | E2E console and failed-request check |
| 4 | Incident import existed only in the API | Import control on the Incidents page | E2E step "export JSON and import it again" |
| 5 | The storage documentation referred to table sizes in the UI that did not exist | Monitoring store card on the Settings page | E2E settings step |
| 6 | Integration packages ran in parallel against one server, so server tests created blocking that CLI tests saw | `go -C backend test -p 1` in the harness | Full matrix |
| 7 | Server test decoded JSON polls into a reused struct, keeping stale pointers | Reset before each decode | — (test code) |
| 8 | E2E expected stored graphs to render without the "Show blocking graph" click | Test clicks it | — (test code) |
| 9 | Grid columns overflowed the viewport at 390 px (fleet cards, overview charts) | Grid items may shrink (`min-width: 0`) | Screenshot overflow check |
| 10 | Count charts showed fractional or repeated axis ticks | Integer ticks for count series | Screenshot review |
| 11 | Tiles showed a truncated "unknown / unknown" | A single "unknown" when both values are unknown | Screenshot review |

New unit tests written during verification: `backend/internal/transport/httpapi` (time ranges,
paging, downsampling bounds, body limits, path IDs, middleware),
`backend/internal/lib/hostmetrics` (parsing, rates, resets, size limit, URL rules),
`backend/internal/domain/targets` (validation without echoing DSNs).

## Not run or not verified

- **GitHub Actions** has not run: nothing was pushed. The workflow is
  untested on GitHub's runners.
- **TLS** to targets and the store was not exercised (lab servers run without
  TLS). Built-in TLS termination does not exist.
- **Host metrics with a real node_exporter** were tested with unit tests and
  an HTTP test server only; `make up-host` was not exercised in this run.
- **Managed services** (RDS, Cloud SQL, Azure) and PostgreSQL forks were not
  tested.
- **Long-running behaviour** (days of retention cleanup, large stores, many
  targets beyond 20) was not measured.
- **Browsers other than Chromium** were not tested.
- Parallel-worker lock waits and a live `missing` placeholder remain
  unverified end to end (see the appendix).

## Reproduction

```bash
make check && make web-check
make test-integration                                   # 14.24 and 18.6
LOCKLENS_IT_PG_VERSIONS="14.24 15.19 16.15 17.11 18.6" ./scripts/integration-test.sh
make e2e
make up && ./scripts/perf-smoke.sh && make down
```

Every script removes only its own Compose project; nothing else is pruned.

---

## Appendix: phase 1 CLI QA run

QA run of the CLI against real PostgreSQL servers earlier on 2026-10-24
(commit `db0d42a` plus the fixes listed below). Checklist:
[testing-plan.md](testing-plan.md).

### Environment

| Item | Value |
| --- | --- |
| Host | macOS 15.7.4, Apple M1 Pro (8 cores), 32 GiB RAM, arm64 |
| Go | go1.26.0 darwin/arm64 (`backend/go.mod`: `go 1.26.0`); unit tests were also run with go1.27.3 in the previous verification round |
| Docker | Engine 29.8.2 (linux/arm64) via OrbStack 2.2.4; Docker Compose 5.5.0 |
| Latest stable PostgreSQL major | 18 (current minor 18.6), per [postgresql.org/support/versioning](https://www.postgresql.org/support/versioning/) on 2026-10-24; 19 was only available as beta/RC images |

PostgreSQL images (official `postgres` images, pinned in the harness and CI):

| Image tag | Digest | `SHOW server_version` |
| --- | --- | --- |
| `postgres:14.24-alpine` | `sha256:50fce0c9c54c43d8b02ce16cb9daff75eb94c09c4ee3d41c157852ebd9700c80` | 14.24 |
| `postgres:15.19-alpine` | `sha256:a8e042598834300dfaa17a54f695b87b7f25d3490433bbbe2ef3bffbb9747f24` | 15.19 |
| `postgres:16.15-alpine` | `sha256:f4610795587492cd4fb887c2dc3de301b813278bb6f616992061141b5f15ac36` | 16.15 |
| `postgres:17.11-alpine` | `sha256:eee6c6e1b0894fa4fe0075eb054af9fd1cf39a35321c2d4110626cfe506411fb` | 17.11 |
| `postgres:18.6-alpine` | `sha256:898c60cb390d1085bc97bdd6a1ae968757421df394b071848b52b1c09c89d6a5` | 18.6 |

Test databases: one isolated Compose project per version
(`locklens-it-<version>-<run id>`), loopback-only dynamic host ports
(observed 32768–32779), `tmpfs` data directory, generated passwords with a
`cnry` canary prefix, `max_prepared_transactions=10`. No `.env` file was read
and no other database was contacted.

### Commands executed

```bash
gofmt -l .                                   # empty output
go -C backend vet ./...
go -C backend vet -tags integration ./...
go -C backend test -count=1 ./...
go -C backend test -race -count=1 ./...
make check                                   # fmt-check, vet, check-separation, test, test-race
make build                                   # bin/locklens, bin/locklens-demo
make test-compat                             # integration suite on 14.24 15.19 16.15 17.11 18.6
LOCKLENS_IT_PG_VERSIONS="14.24 18.6" ./scripts/integration-test.sh   # rounds while fixing defects, and a final run on the final tree (PASS on both)
make demo && make db-down                    # demo Compose project, then removal
script -q <file> bin/locklens watch --interval 1s --min-tx-age 1s    # pseudo-terminal check, then SIGINT
docker compose ls -a --filter name=locklens-it                       # leftover check after every run
```

### Results

#### Static checks and unit tests

| Check | Result |
| --- | --- |
| `gofmt -l .` | PASS (no files) |
| `go -C backend vet ./...`, `go -C backend vet -tags integration ./...` | PASS |
| `go -C backend test -count=1 ./...` (8 packages with tests) | PASS |
| `go -C backend test -race -count=1 ./...` | PASS |
| `make check-separation` (CLI does not import `demo/`) | PASS |
| Binary build (`make build`) | PASS |

#### Integration matrix

35 tests per version (34 in `tests/e2e/cli`, plus
`TestSessionEndingMidCollection` in `backend/internal/lib/postgres`; the unit test in that
package also runs). All run with `-race`, against a binary built with `-race`.

| Version | Tests | Result | Duration |
| --- | --- | --- | --- |
| 14.24 | 35 | PASS (35/35) | 105 s |
| 15.19 | 35 | PASS (35/35) | 104 s |
| 16.15 | 35 | PASS (35/35) | 104 s |
| 17.11 | 35 | PASS (35/35) | 104 s |
| 18.6 | 35 | PASS (35/35) | 103 s |

#### Scenario matrix

Status per version: P = PASS, B = BLOCKED, N = NOT RUN.

| Area | Scenario (test) | 14 | 15 | 16 | 17 | 18 |
| --- | --- | --- | --- | --- | --- | --- |
| A | Healthy database: doctor, inspect table/JSON, empty graph (`TestHealthyDatabase`) | P | P | P | P | P |
| A | Own observer connections excluded (`TestOwnConnectionsExcluded`) | P | P | P | P | P |
| B | Row blocking A → B, counts, disappears after release (`TestRowBlocking`) | P | P | P | P | P |
| C | Chain A → B → C, ground truth first, unique counts (`TestBlockingChain`) | P | P | P | P | P |
| D | Fan-out, four waiters (`TestFanOut`) | P | P | P | P | P |
| D | Multiple blockers on one waiter (`TestMultipleBlockers`) | P | P | P | P | P |
| D | Lock queue / soft block (`TestLockQueueSoftBlock`) | P | P | P | P | P |
| E | Idle in transaction, ages, threshold (`TestIdleInTransactionAndThreshold`) | P | P | P | P | P |
| E | Lock wait age from `waitstart`, not `query_start`/`state_change` (`TestLockWaitAgeComesFromWaitStart`) | P | P | P | P | P |
| F | Plain role, then `pg_read_all_stats`, then `pg_monitor` (`TestRestrictedPermissions`) | P | P | P | P | P |
| G | Prepared transaction (PID 0), `ROLLBACK PREPARED` verified (`TestPreparedTransactionBlocker`) | P | P | P | P | P |
| H | Session churn during watch/inspect/in-process collections (`TestSessionsDisappearingDuringCollection`) | P | P | P | P | P |
| H | Blocker terminated mid-collection, deterministic (`TestSessionEndingMidCollection`) | P | P | P | P | P |
| — | Observer connected to another database (`TestCrossDatabaseObservation`) | P | P | P | P | P |
| CLI | Help and version (`TestHelpAndVersion`) | P | P | P | P | P |
| CLI | DSN precedence, missing DSN (`TestDSNPrecedence`) | P | P | P | P | P |
| CLI | Invalid flags and durations exit 2 (`TestInvalidArgumentsExitTwoBeforeConnecting`) | P | P | P | P | P |
| CLI | Connection refused (`TestConnectionRefused`) | P | P | P | P | P |
| CLI | Authentication failure, URI and keyword/value; watch stops (`TestAuthenticationFailureURIAndKeyValue`) | P | P | P | P | P |
| CLI | Keyword/value DSN works (`TestKeyValueDSNWorks`) | P | P | P | P | P |
| CLI | Malformed DSN never echoed (`TestMalformedDSNIsNeverEchoed`) | P | P | P | P | P |
| CLI | Connect timeout against a silent listener (`TestConnectTimeout`) | P | P | P | P | P |
| CLI | Collection timeouts: `statement_timeout` and `lock_timeout` (`TestCollectionTimeouts`) | P | P | P | P | P |
| CLI | JSON only on stdout (`TestStdoutCarriesOnlyJSON`) | P | P | P | P | P |
| CLI | Unicode and terminal control characters, rune truncation (`TestUnicodeAndControlCharacters`) | P | P | P | P | P |
| Security | Query-text canary in table, JSON, NDJSON, snapshot (`TestQueryTextCanary`) | P | P | P | P | P |
| Security | Password/DSN canaries in every subprocess output (all tests, `checkOutput`) | P | P | P | P | P |
| Snapshot | Save, strict schema, 0600, no overwrite, offline show (`TestSnapshotSaveAndShowOffline`) | P | P | P | P | P |
| Snapshot | Malformed, future version, foreign JSON, wrong type, missing, directory (`TestSnapshotRejectsBadFiles`) | P | P | P | P | P |
| Snapshot | Failed writes leave nothing behind (`TestSnapshotFailedWriteLeavesNothing`) | P | P | P | P | P |
| Watch | Five NDJSON frames, SIGINT exit 0, empty stderr (`TestWatchNDJSONAndInterrupt`) | P | P | P | P | P |
| Watch | Redirected table without escape codes, SIGTERM exit 0 (`TestWatchRedirectedTableHasNoEscapeCodes`) | P | P | P | P | P |
| Watch | Connections bounded, no overlapping collections (`TestWatchConnectionsBoundedWithoutOverlap`) | P | P | P | P | P |
| Watch | Container paused/unpaused: backoff and recovery (`TestWatchSurvivesTemporaryOutage`) | P | P | P | P | P |
| Overhead | Repeated collections smoke check (`TestObserverOverheadSmoke`) | P | P | P | P | P |

Manual checks (PostgreSQL 17.11 demo database, executed once):

| Check | Result |
| --- | --- |
| `make demo`: A → B, A → B → C (1 direct, 2 total), idle in transaction (3 s) | PASS |
| `watch` on a pseudo-terminal (`script`): screen cleared per frame (5 frames), SIGINT exit 0, no error text | PASS |
| Documented cleanup command for a leftover `locklens-it-*` project removes it and its volumes; port bound to `127.0.0.1` | PASS |

Graph shapes not reproduced on a live server (shared descendants, duplicate
edges, cycles, self loops) are covered by unit tests in `backend/internal/domain/graph`,
`backend/internal/domain/report`, and `backend/internal/lib/render` (PASS). No scenario was BLOCKED.

#### Selected observations

- **Ground truth.** In every blocking scenario `pg_blocking_pids()` queried
  directly matched LockLens' edges. In the lock-queue scenario PostgreSQL
  reported C as blocked only by B (soft block), and LockLens showed A → B → C.
- **Watch outage.** With the container paused, each version logged
  `attempt=1 retry_in=500ms`, `attempt=2 retry_in=1s`, `attempt=3 retry_in=2s`
  (timeouts after `--query-timeout 2s`), then `collection recovered
  after_failures=3` within about 2 s of unpausing. LockLens connections after
  recovery: peak 1.
- **No overlap.** About 250–270 samples of `pg_stat_activity` per version
  during a 6 s watch at 500 ms: at most 1 LockLens connection, at most 1 busy.
  All connections closed after exit.
- **Churn.** 909–1,199 reports per version while actors were terminated
  repeatedly (437–542 contained edges): no failed collection, no invalid JSON,
  no `missing` placeholder. See the consistency notes in
  [postgresql-observation.md](postgresql-observation.md#consistency-of-a-collection).

### Observer overhead smoke check

`TestObserverOverheadSmoke`: one collector, 20 warm-up collections, then 300
sequential collections against a quiet, isolated server (no contention,
default `--query-max-length 200`, no query text), over Docker's loopback port
publishing on the host described above. Latency is measured on the client
around `Collect`.

| Version | p50 ms | p95 ms | p99 ms | max ms | max LockLens connections | goroutines after warm-up → after run |
| --- | --- | --- | --- | --- | --- | --- |
| 14.24 | 1.63 | 2.09 | 2.84 | 6.22 | 1 | 3 → 3 |
| 15.19 | 1.45 | 1.69 | 1.86 | 2.08 | 1 | 3 → 3 |
| 16.15 | 1.81 | 2.59 | 5.11 | 36.72 | 1 | 3 → 3 |
| 17.11 | 1.86 | 2.36 | 3.62 | 7.36 | 1 | 3 → 3 |
| 18.6 | 1.88 | 3.31 | 10.84 | 47.19 | 1 | 3 → 3 |

All LockLens connections were gone after `Close`. Limits of these numbers:
a single run per version on a laptop with other workloads, a quiet server with
a handful of backends, and a race-enabled test binary. Occasional maxima of
36–47 ms are single outliers. They say nothing about servers with many
backends or many waiting sessions (where `pg_blocking_pids()` is called once
per waiting backend) and are **not** a benchmark or a performance guarantee.

### Defects found and fixed

| # | Defect | Kind | Fix | Regression test |
| --- | --- | --- | --- | --- |
| 1 | A lock on a relation in another database was rendered as `on relation relation OID 16384 in database x` (duplicated word); the unit test had codified the wrong text | Output defect | `DescribeLock` renders `on relation OID 16384 in database x` (also for `extend` locks) | `TestDescribeLock` (corrected and extended), `TestCrossDatabaseObservation` |
| 2 | `snapshot show <directory>` exited 1 with `read ...: is a directory`, contradicting the documented exit code 2 for invalid input files | Exit-code defect | Directories are rejected as usage errors: `X is a directory, not a snapshot file` | `TestSnapshotShowBadFiles` (unit), `TestSnapshotRejectsBadFiles` (integration) |
| 3 | Ctrl+C during the connection phase printed `connect to ...: context canceled`, while Ctrl+C during collection printed `interrupted` | Inconsistent message | Both report `locklens: error: interrupted` (exit 1); documented | `TestInterruptedDuringConnectIsReportedAsInterrupted` |
| 4 | Documentation stated that sessions ending mid-collection become `missing` placeholders. Live testing showed PostgreSQL caches `pg_stat_activity` at its first read (inside the blocking query), so such sessions keep their metadata and edges; only their live `pg_locks` rows disappear | Documentation defect | Corrected postgresql-observation.md, limitations.md, blocking-graph.md, and the `missing_session` warning and placeholder texts | `TestSessionEndingMidCollection` (uses a new unexported test hook in the collector) |
| 5 | The rollback that ends each collection used a context without a deadline, so a server freezing at that moment could hang the command | Robustness (found by review, not reproduced) | Rollback bounded to 5 s | None dedicated (the hang was not reproduced); the outage test still passes |

Test infrastructure changes (not product defects):

- The previous integration tests defaulted to the demo database on port
  55432 when no DSN was set and shared it with the demo. They now run only
  through `scripts/integration-test.sh` in isolated projects and refuse to run
  without explicit DSNs.
- `TestNoBlocking` previously skipped when other blocking existed; on an
  isolated server it is now a strict assertion (`TestHealthyDatabase`).
- `backend/demo/scenario` gained exported building blocks (`New`, `Begin`, `Exec`,
  `ExecAsync`, `WaitDone`, `Release`, `Ident`) for custom scenarios.

Missing functionality: none of the required MVP features was missing.

### Remaining limitations

- GitHub Actions has not run: nothing was pushed. The workflow uses the same
  harness and version list but is untested on GitHub's runners.
- Parallel-worker lock waits were not reproduced on a live server; the
  folding of workers into leaders is implemented but untested end to end.
- A `missing` placeholder could not be produced against a live server (see
  defect 4); it is covered by unit tests only.
- Connection timeouts were tested with a silent local listener, not with a real
  network partition; the outage test pauses the container instead.
- TLS (`sslmode=require`/`verify-full`) was not exercised: the test servers run
  without TLS. LockLens leaves TLS to pgx and does not change `sslmode`.
- Overhead figures are a local smoke check only (see above).
- PostgreSQL 14 reaches end of life on 2026-11-12.

### Reproduction and cleanup

```bash
git clone <repository> locklens && cd locklens
make check                      # no database needed
make test-integration           # PostgreSQL 14.24 and 18.6
make test-compat                # 14.24 15.19 16.15 17.11 18.6
```

The harness removes each Compose project with `docker compose -p <project> down -v`
on success, failure, or Ctrl+C, and logs to `test-results/`. If a run is killed
hard, find and remove leftovers by exact name:

```bash
docker compose ls -a --filter name=locklens-it
```

```bash
LOCKLENS_IT_IMAGE=x LOCKLENS_IT_ADMIN_USER=x LOCKLENS_IT_ADMIN_PASSWORD=x LOCKLENS_IT_DATABASE=x docker compose -p <project name> -f deploy/compose.test.yaml down -v
```

Demo:

```bash
make demo         # project "locklens" on 127.0.0.1:55432
make db-down      # docker compose down -v for that project
```

After this run, `docker compose ls -a` listed no `locklens` or `locklens-it-*`
projects and no volumes labelled with those projects remained. Unrelated
Compose projects on the host were not touched.
