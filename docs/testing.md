# Testing

How LockLens is tested. The QA checklist is in
[testing-plan.md](testing-plan.md) and the results of the latest run are in
[test-report.md](test-report.md).

## Layers

| Layer | Where | Needs a database | What it proves |
| --- | --- | --- | --- |
| Graph algorithms | `backend/internal/domain/graph` | no | Counts, roots, cycles, duplicates, shared descendants, determinism |
| Analysis | `backend/internal/domain/report` | no | Node kinds, the three ages, thresholds, NULL handling, warnings |
| Rendering | `backend/internal/lib/render` | no | Sanitising, shared nodes, cycles terminate, query opt-in, NDJSON lines |
| Text safety | `backend/internal/lib/textsafe` | no | Rune truncation, control and bidi characters |
| Configuration | `backend/internal/config` | no | DSN precedence, observer settings, redaction, unparsable DSNs never echoed |
| Snapshot files | `backend/internal/lib/snapshot` | no | Round trip, no-clobber, atomic writes, versions, corrupt files, docs example |
| CLI behaviour | `backend/internal/transport/cli` | no | Exit codes, offline `snapshot show`, `watch` retries and clean cancel |
| Error hints | `backend/internal/lib/postgres` | no | SQLSTATE classification for hints and fatal errors |
| Counters and writer | `backend/internal/domain/collector` | no | Deltas and rates, unknown on first sample/restart/reset/decrease/gap, created and dropped scopes, bounded queue drops the oldest, retries |
| Rules | `backend/internal/domain/rules` | no | Persistence, streaks broken by gaps and PID reuse, insufficient data never passes, every rule's thresholds |
| Auth and secrets | `backend/internal/domain/auth`, `backend/internal/lib/secret` | no | Sessions, rate limiting, CSRF and Origin, password policy; AES-GCM round trip, tampering, wrong key and context |
| API helpers and middleware | `backend/internal/transport/httpapi` | no | Time ranges, paging, downsampling bounds, body limits and unknown fields, path IDs, security headers, request IDs, 401/404/405 |
| Targets, host metrics | `backend/internal/domain/targets`, `backend/internal/lib/hostmetrics` | no | Validation without echoing DSNs; exporter parsing, rates, resets, size limit, URL rules |
| Exports, metrics | `backend/internal/domain/export`, `backend/internal/lib/obs` | no | Query text scrubbed by default, standalone escaped HTML, import validation; exposition format, label cardinality guard |
| Collector edge case | `backend/internal/lib/postgres` (tag `integration`) | yes | A session terminated in the middle of a collection |
| Monitoring store | `backend/internal/repository` (tag `integration`) | yes | Concurrent idempotent migrations, sessions, soft delete and purge, query aggregation and coverage, batched retention, findings sync |
| Incident recorder | `backend/internal/domain/incidents` (tag `integration`) | yes | Persistence, deduplication, resolution, cooldown regrouping, root changes, restart recovery, size and duration limits |
| CLI end to end | `tests/e2e/cli` (tag `integration`) | yes | Real contention observed through the built binary |
| Server end to end | `tests/e2e/server` (tag `integration`) | yes | `locklens server` in process, exercised only over HTTP |
| Frontend | `web/src/**/*.test.{ts,tsx}` (Vitest, Testing Library, axe-core) | no | Unknown vs. zero, statement-type rates and gaps, query mix, login, overview, queries filtering, data-access setup with confirmation, accessibility |
| Browser E2E | `tests/e2e/browser` (Playwright) | Docker stack | The main user path against the real application, no API mocks |

## Integration environment

`scripts/integration-test.sh` (also `make test-integration`) runs the
database-backed tests. For each PostgreSQL version it:

1. writes a private Compose env file with a pinned image
   (`postgres:<major>.<minor>-alpine`) and generated throwaway passwords;
   no `.env` file is read;
2. starts [`deploy/compose.test.yaml`](../deploy/compose.test.yaml) under a unique project
   name (`locklens-it-<version>-<run id>`), publishing PostgreSQL on
   `127.0.0.1` with a host port chosen by Docker, data on `tmpfs`,
   `max_prepared_transactions=10`, and `pg_stat_statements` preloaded; a hot
   standby (Compose profile `replication`) streams from it;
3. waits for the health check with `docker compose up --wait --wait-timeout 120`
   (a TCP `pg_isready`, so the entrypoint's temporary init server does not
   count as ready);
4. creates the roles `demo_app` (writes contention), `locklens_observer`
   (`pg_read_all_stats`), `locklens_limited` (no monitoring privileges), and
   `replicator`, plus a database `locklens_pgss` with the
   `pg_stat_statements` extension (the default test database has none, so
   both the available and the unavailable case are covered); builds
   [HypoPG](https://github.com/HypoPG/hypopg) 1.4.4 from source into the
   disposable server and creates it in `locklens_pgss` (best effort: without
   network the HypoPG success path is skipped and the run says so);
5. creates a temporary Go workspace containing `backend/` and `tests/e2e/`,
   then runs `go test -tags integration -race -p 1` from the repository root for `./tests/e2e/cli/...`,
   `./backend/internal/lib/postgres/...`, `./backend/internal/repository/...`,
   `./backend/internal/domain/incidents/...`, `./backend/internal/domain/optimizer/...`, and `./tests/e2e/server/...` with explicit DSNs in
   the environment. Packages run one at a time because they share the
   database server;
6. removes the project with `docker compose -p <project> down -v`, also when
   tests fail or the script is interrupted.

The tests refuse to start without the `LOCKLENS_IT_*` variables, so they can
never fall back to another database. Subprocesses get a scrubbed environment
(no inherited `LOCKLENS_*` or `PG*` variables) and a timeout.

```bash
make test-integration                                        # 14.24 and 18.6
make test-compat                                             # 14.24 15.19 16.15 17.11 18.6
LOCKLENS_IT_PG_VERSIONS="17.11" ./scripts/integration-test.sh
LOCKLENS_IT_KEEP=1 ./scripts/integration-test.sh             # keep containers; prints the down command
```

| Variable | Default | Purpose |
| --- | --- | --- |
| `LOCKLENS_IT_PG_VERSIONS` | `14.24 18.6` | Image versions to test |
| `LOCKLENS_IT_GO_TIMEOUT` | `15m` | `go -C backend test -timeout` |
| `LOCKLENS_IT_WAIT_TIMEOUT` | `120` | Seconds to wait for the health check |
| `LOCKLENS_IT_ARTIFACTS` | `test-results/` | Logs and the overhead JSON |
| `LOCKLENS_IT_KEEP` | `0` | `1` keeps the containers |
| `LOCKLENS_IT_PACKAGES` | all five packages across both Go modules | Narrow the Go packages, e.g. `./tests/e2e/server/...` |
| `LOCKLENS_IT_RUN` | `.` | `go -C backend test -run` filter |

Variables set by the script for the tests: `LOCKLENS_IT_ADMIN_DSN`,
`LOCKLENS_IT_DEMO_DSN`, `LOCKLENS_IT_OBSERVER_DSN`, `LOCKLENS_IT_LIMITED_DSN`,
`LOCKLENS_IT_PGSS_OBSERVER_DSN`, `LOCKLENS_IT_PGSS_DEMO_DSN`,
`LOCKLENS_IT_STANDBY_OBSERVER_DSN`,
`LOCKLENS_IT_COMPOSE_PROJECT`, `LOCKLENS_IT_COMPOSE_FILE`,
`LOCKLENS_IT_COMPOSE_ENV_FILE`, `LOCKLENS_IT_PG_VERSION`,
`LOCKLENS_IT_SERVER_VERSION`. The admin (superuser) DSN is used only for test
setup, independent checks of PostgreSQL state, and the outage simulation; the
CLI under test never receives it.

To remove a kept project manually, use the exact command the script prints,
for example:

```bash
docker compose -p locklens-it-18-6-<run id> -f deploy/compose.test.yaml --env-file <printed path> down -v
```

## Conventions in the integration suite

- **Dedicated connections.** Each actor (A, B, C, ...) has its own connection;
  its PID is read from that connection. PIDs are never hard-coded.
- **Ground truth first.** Blocking scenarios check `pg_blocking_pids()`
  directly with the admin connection before asserting LockLens output.
- **State polling, not sleeps.** After starting a statement that should block,
  `backend/demo/scenario` polls `pg_blocking_pids()` every 25 ms; idle scenarios poll
  `pg_stat_activity` until the session is idle in transaction and old enough
  by the server clock. Polling gives up after 15 s.
- **Strict JSON.** Reports are decoded with unknown fields disallowed, so
  output must match the documented types exactly.
- **Global output checks.** Every LockLens subprocess output is searched for
  all passwords and DSNs of the run (they carry a `cnry` canary prefix), for
  race detector reports, and for panics. The binary under test is built with
  `-race`.
- **Bounded everything.** 90 s per test context, 30 s per subprocess by
  default, 15 min per `go test` run.
- **Cleanup.** Scenarios release sessions in creation order, cancel stuck
  statements after 5 s, close connections, and run `ROLLBACK PREPARED` for
  prepared transactions. Demo sessions also set `lock_timeout = 5min` and
  `idle_in_transaction_session_timeout = 10min` as a server-side safety net.
  Each test uses its own table.

## Integration scenarios

| Test | Scenario | Key assertions |
| --- | --- | --- |
| `TestHealthyDatabase` | A. idle server | doctor all OK, `No blocking detected`, empty graph and zero summary in JSON |
| `TestOwnConnectionsExcluded` | A. | LockLens' collecting session never listed, even with `--min-tx-age 0s` |
| `TestRowBlocking` | B. row lock A → B | `pg_blocking_pids(B) = [A]`; only edge A → B; counts 1/1; B waits on `transactionid`; blocking disappears after A is released |
| `TestBlockingChain` | C. A → B → C | ground truth first; no A → C; counts 2/1/0; one component rooted at A |
| `TestFanOut` | D. one `ACCESS EXCLUSIVE` holder, four waiters | exactly four edges, no duplicate nodes, direct = transitive = 4 |
| `TestMultipleBlockers` | D2. two `SHARE` holders block one `UPDATE` | `blocked_by = [A, B]`, both are roots |
| `TestLockQueueSoftBlock` | D3. `ACCESS SHARE` queued behind a pending `ACCESS EXCLUSIVE` | soft block reported as A → B → C, C waits for `AccessShareLock` on the table |
| `TestIdleInTransactionAndThreshold` | E. idle in transaction | state, `xact_start` equals `now()` of the transaction, age equals `server_time − xact_start`, query age measured from `query_start`, no lock wait, threshold filtering |
| `TestLockWaitAgeComesFromWaitStart` | E. lock wait vs. query age | `wait_start` equals `pg_locks.waitstart`; lock wait is ≥ 0.5 s shorter than the ages from `query_start` and `state_change` |
| `TestRestrictedPermissions` | F. dedicated role | without privileges: edges visible, details NULL, identifying columns visible, other roles' idle transactions invisible, warnings, doctor warns; with `pg_read_all_stats` and then `pg_monitor`: full visibility, no warnings |
| `TestPreparedTransactionBlocker` | G. PID 0 | edge 0 → B kept, node kind `prepared_transaction`, GID listed, `ROLLBACK PREPARED` verified |
| `TestSessionsDisappearingDuringCollection` | H. churn | actors terminated repeatedly while `watch`, `inspect`, and ~1,000 in-process collections run: no failures, every report valid |
| `TestSessionEndingMidCollection` | H. deterministic | blocker terminated between catalog reads: edge kept, session reported with cached metadata, live lock request gone |
| `TestCrossDatabaseObservation` | observer in another database | graph intact; relation reported by OID and database name only |
| `TestHelpAndVersion` | CLI | help lists commands and flags; version text and JSON |
| `TestDSNPrecedence` | CLI | `--dsn` beats `LOCKLENS_DSN` both ways; `PG*` variables do not replace a missing DSN |
| `TestInvalidArgumentsExitTwoBeforeConnecting` | CLI | 13 invalid invocations exit 2 with empty stdout |
| `TestConnectionRefused`, `TestConnectTimeout` | CLI | exit 1 with hints; a silent listener times out after `--connect-timeout` |
| `TestAuthenticationFailureURIAndKeyValue` | CLI | exit 1, hint, wrong password never echoed; `watch` stops instead of retrying |
| `TestKeyValueDSNWorks`, `TestMalformedDSNIsNeverEchoed` | CLI | keyword/value DSNs work; malformed DSNs exit 2 without echoing |
| `TestCollectionTimeouts` | CLI | with `pg_locks` locked by another session: `--query-timeout 1s` and the 2 s `lock_timeout` both fail fast with exit 1; doctor fails the `read pg_locks` check |
| `TestStdoutCarriesOnlyJSON` | CLI | stdout is exactly one JSON document |
| `TestUnicodeAndControlCharacters` | CLI | ESC/BEL/bidi characters escaped in tables; exact text in JSON; rune truncation including a cut after a three-byte character |
| `TestQueryTextCanary` | security | marker absent from table, JSON, NDJSON, and snapshot by default; present with `--include-query`; absent for a role without privileges |
| `TestSnapshotSaveAndShowOffline` | snapshots | strict schema decode, UTC timestamps, mode 0600, no overwrite without `--force`, offline show with an unreachable DSN |
| `TestSnapshotRejectsBadFiles` | snapshots | truncated, future version, foreign JSON, wrong type, missing file, directory: exit 2 |
| `TestSnapshotFailedWriteLeavesNothing` | snapshots | read-only and missing directories: exit 1, no destination, no temporary file |
| `TestWatchNDJSONAndInterrupt` | watch | five NDJSON lines, each a complete report; SIGINT exits 0 with empty stderr |
| `TestWatchRedirectedTableHasNoEscapeCodes` | watch | frame separators, no escape codes; SIGTERM exits 0 |
| `TestWatchConnectionsBoundedWithoutOverlap` | watch | at most 2 connections and 1 busy connection over ~260 samples; all closed after exit |
| `TestWatchSurvivesTemporaryOutage` | watch | container paused: retries logged with backoff 500ms/1s/2s; unpaused: `collection recovered`, frames resume, connections bounded |
| `TestObserverOverheadSmoke` | overhead | 300 sequential collections; latency percentiles; connections and goroutines stay flat |

## Server integration scenarios

`tests/e2e/server` starts `server.Run` in process with a fresh store database and
talks to it only over HTTP. Every response is checked for all passwords and
DSNs of the run.

| Test | Key assertions |
| --- | --- |
| `TestAuthAndSecurityHeaders` | 401 without a session; 403 for a mutation without CSRF token and for a cross-origin request with a valid token; CSP and other headers; oversized bodies and ranges rejected; `/metrics` without DSNs |
| `TestTargetLifecycleBlockingIncidentAndExport` | DSN validation does not echo the password; healthy and unreachable targets side by side (isolation); `pg_stat_statements` reported unavailable with a reason; connection test; a real A → B → C chain in live activity, with SQL text (collected by default); a blocking incident opens; restart while it is open keeps history and records `recorder_restarted`; releasing the chain resolves it; acknowledging does not change evidence; JSON export without query text, standalone HTML without scripts or URLs; import round trip; soft delete keeps incidents |
| `TestRestrictedRoleAndQueryTextOptIn` | a role without `pg_read_all_stats` gives a `degraded` target with a `restricted_visibility` warning; query text appears only for the opted-in target and disappears when the option is turned off |
| `TestPgStatStatementsAvailable` | extension detected; `demo_app` statements appear with call counts, normalised SQL text by default and command class `select`; `include_text=false` omits text; `/queries/commands` reports select totals and coverage buckets; unknown command classes are rejected; query IDs are decimal strings |
| `TestQueryPerformanceIntelligence` | a real `pg_sleep(90)` appears under `/performance` as `queries.long_running` with metrics, impact components, and confidence; at the critical threshold it opens an incident whose investigation is available, lists missing evidence, and suggests one heavy execution; statement insight reports a learning baseline, bands, and percentiles as unavailable; pasted plans are saved, compared (shape changed), give an index candidate, and are deleted; an invalid plan is rejected |
| `TestQueryOptimizer` | a schema with a foreign key without index, a duplicate index, and an invalid index (from a failed concurrent unique build) plus a real workload: "Analyse now" collects a fresh catalog and derives the foreign-key, duplicate, invalid, and missing-index recommendations with their statements and SQL; a decision is stored and an invalid one refused; the what-if check with HypoPG plans the statement with the hypothetical index (used, cheaper, confidence high on PostgreSQL 16+; refused without hypopg) and leaves no index behind; the statement page shows the parser's predicates and the recommendation; creating the index makes the missing-index and foreign-key recommendations applied |
| `TestChangesTokensAndLogIngest` | a pipeline reports a deploy with an `events` token; a log agent's `ingest` token ships events (stored once when sent twice, rejected for another scope, a bad token, or an unknown kind); the instance's log summary has the statement's percentiles and the agent, the other instance none; token secrets are never listed |
| `TestTwoServersShareTheStore` | two servers on one store split two instances by leases, both answer with live data for both, and when one stops the other collects both |
| `TestPrimaryAndStandby` | standby detected; primary lists the replica and slot; standby reports a streaming WAL receiver |
| `TestOutageAndRecovery` | the database container (target and store) paused for 25 s; the target becomes healthy again and a collection gap is recorded |

## Frontend tests

```bash
make web-check            # typecheck, ESLint, Vitest
cd web && npm run build   # production build
```

Tests render with real React Router and TanStack Query and a stubbed `fetch`:

- formatting never turns unknown into 0; statement types keep fixed colors;
- statement-type rates divide by covered time, quiet covered buckets are zero,
  uncovered or missing buckets are gaps (`commandSeries`);
- the query mix table (shares, averages, empty state);
- sign-in with a rejected and an accepted password;
- the overview shows "—" for unknown values and the query mix;
- the queries page offers only the statement types present and filters by type;
- the data explorer asks for separate credentials and confirms row access
  before saving;
- the Optimize page lists recommendations by benefit, shows the evidence and
  SQL in its drawer, hides the what-if check when HypoPG is off, records a
  decision, and says what the optimizer cannot see (no catalog, no query
  text, no parser); the statement page shows parser lints and its
  recommendations;
- the Performance page ranks anomalies by impact, explains the score in its
  drawer, lists running queries separately, and says when nothing is
  anomalous; hypotheses show evidence for and against and missing evidence;
- axe-core finds no serious or critical violations on the rendered pages.

## Browser end-to-end

`scripts/e2e.sh` (also `make e2e`) starts `deploy/compose.yaml` with the lab overlay `deploy/compose.lab.yaml` under a unique
project name on random loopback ports, runs `tests/e2e/browser` with Playwright
(Chromium), and removes only that project afterwards
(`LOCKLENS_E2E_KEEP=1` keeps it). Nothing in the main path mocks or intercepts
the API. The test:

1. signs in (a wrong password is rejected first), lands on the fleet
   Overview, and checks that no token is kept in web storage;
2. sees the six KPIs, every Overview card, and the standby under its primary
   in the topology card;
3. opens `lab-primary` from Databases and sees its current values and the
   queries-by-type chart;
4. starts the `shop` lab scenario and waits until the instance's Queries tab
   shows SELECT, INSERT, UPDATE, and DELETE in the breakdown; filters by UPDATE,
   opens a statement with its SQL and performance analysis (learning baseline,
   no percentiles), saves a pasted plan and reads its index candidate, finds
   the statement in Queries across instances, opens Performance, and on
   Optimize collects a fresh catalog ("Analyse now") and opens a
   recommendation's evidence when there is one;
5. browses `shop.orders` in the Explorer: rows, a filter (`status = paid`,
   every row checked), a cell panel, the Schema and Triggers tabs, and the
   functions of the schema;
6. runs a SELECT in the SQL Editor (10 rows), draws its execution plan, and
   checks that an UPDATE is refused and changed nothing;
7. starts the `chain` scenario, finds it under Blocking, opens a session panel
   under Sessions, the blocking tree and the Hot tables (with `shop.*` tables)
   on the instance's Activity tab, then the
   incident under Alerts and its stored blocking graph;
8. exports the HTML report (no scripts or credentials) and opens it in an
   offline browser context with JavaScript disabled, checking it loads nothing;
9. exports JSON and imports it again under Settings;
10. adds an instance from the lab's superuser connection string: the plan
    lists the `CREATE ROLE`/`GRANT` statements without the password, the
    roles are created after confirmation, and the instance is stored with
    `locklens_monitor`; the advanced form still tests a connection;
11. adds a viewer under Settings → Users and roles, signs in as that viewer in a second
    browser context, and checks that the SQL Editor and Settings are not
    offered and that the API refuses SQL and the users list with 403;
12. checks Databases, standby replication (streaming), Topology, Connections,
    Incidents, Settings store sizes, and the other theme;
13. runs axe-core, including color contrast, on every page (serious and
    critical violations fail);
14. signs out from the account menu and checks the API returns 401;
15. fails on any browser console error or failed request (except the expected
    401s before signing in and the refused UPDATE's 400).

Reports and traces are written to `test-results/e2e-report` and
`test-results/e2e-artifacts`.

## Performance smoke check

`scripts/perf-smoke.sh` needs the lab (`make lab-up`). It starts a separate server
with a temporary store database and `PERF_TARGETS` targets (default 20, half
on the primary, half on the standby), runs for `PERF_DURATION` seconds
(default 300) with production intervals, and reports collection durations,
peak concurrent collector runs versus the limit, connections per database
(from the process's own sockets), memory, CPU time, write-queue drops, and
store growth. Results: `test-results/perf-smoke.txt`; the latest numbers are
in [test-report.md](test-report.md). They describe one local machine, not a
capacity guarantee.

Graph shapes that are hard to create deterministically on a live server
(shared descendants, duplicate edges, cycles, self loops) are covered by unit
tests in `backend/internal/domain/graph`, `backend/internal/domain/report`, and `backend/internal/lib/render`. A cycle is
always reported as a cycle and never as a confirmed deadlock.

## Unit test scenarios

- Graph: chain, fan-out, multiple blockers, shared descendants counted once,
  duplicate edges, PID 0, two-node cycle, cycle fed by an unblocked entry, self
  loop, component ordering, a 2,000-node cycle, deterministic output.
- Analysis: missing and prepared-transaction nodes, cycle wording, NULL
  timestamps, lock wait unknown without `waitstart`, inclusive threshold, only
  client backends as long transactions, aborted idle transactions, clamping.
- Redaction: URL and keyword/value passwords, URL-encoded variants,
  `PGPASSWORD`, wrapped errors, four malformed DSNs.
- Text: multi-byte and emoji truncation, invalid UTF-8, ESC/OSC/CSI, C1, DEL,
  bidi overrides.
- SQL parser (cgo): predicates with tables resolved through aliases, joins,
  CTEs and subqueries, flipped comparisons, sort order, and every lint; the
  stub without cgo reports the parser unavailable.
- Optimizer: index proposals (column order by selectivity, merged across
  statements, skipped when covered or for small tables), foreign keys without
  index, unused (confidence by statistics age), redundant, and invalid
  indexes, stale statistics, misestimates with extended statistics, spills
  with `work_mem`, statement shapes, identifier quoting. Integration: applied
  changes detected from the catalog and judged from the statement history
  (improved and regressed), and no decision after a change.
- Collection: pacing slows a collector over its time budget and recovers;
  bursts shorten intervals at once and are extended without waking again;
  table activity rates skip reset counters and keep the 20 busiest tables.
  Repository: metric series from rollups equal those from raw points (fully,
  partly, and not rolled up, with an unknown sample), hourly steps read hourly
  rollups, and retention keeps hourly rollups after minute ones expire.
- Baselines: median and MAD, the zero-variance floor, quiet covered periods
  counted as zero, reset periods skipped, hour-of-day profiles.
- Statement rules: latency regression against the baseline (critical ratio,
  confidence, evidence basis), a slow hour of day that is usual, warm-up
  falling back to the previous window, too few calls, query storm, load, I/O,
  rows-per-call pattern, possible N+1 (only parameterised SELECTs), long-running
  thresholds and baseline-based expectation, idle sessions and parallel
  workers ignored, one key per execution.
- Insight: impact components bounded and explained, missing measurements add
  nothing; correlation ranks lock contention, plan change, traffic, and shared
  pressure from their evidence, is deterministic, and says when nothing points
  to an explanation. Plans: JSON and text parsing, fingerprints ignore costs,
  comparison of scan changes, index candidates.
- CLI: invalid invocations, connection refused, doctor JSON on failure,
  `snapshot save` refusing an existing file before connecting, bad snapshot
  files (including a directory), `watch` retrying a closed port and exiting 0
  on cancellation, an interrupted connection reported as `interrupted`.

## Demo and lab

The CLI demo and the lab use [`deploy/compose.lab.yaml`](../deploy/compose.lab.yaml) on top of [`deploy/compose.yaml`](../deploy/compose.yaml)
(project `locklens`, fixed loopback ports), described in
[quickstart.md](quickstart.md). They are for people, not for the test suite;
only the E2E script starts that file, under its own project name and ports.

```bash
make demo        # build, start the lab primary, run row-lock, chain, and idle
make lab-down    # remove the lab containers and volumes
```

## PostgreSQL versions

The suite has been run against the pinned images listed in
[test-report.md](test-report.md#environment); run it locally with
`make test-integration` (or `make test-compat` for 14 through 18). CI
([`.github/workflows/ci.yml`](../.github/workflows/ci.yml)) runs only the fast
checks (Go formatting, vet, separation, build, unit tests with and without the
race detector; frontend typecheck, lint, unit tests, build) and then builds and
pushes the Docker image on pushes to `main` and `v*` tags. The integration
suite, the browser E2E suite, and the demo scenarios are not part of CI; run
`make test-integration`, `make e2e`, and `make demo` before a release.

PostgreSQL 14 reaches end of life on 2026-11-12 according to the
[PostgreSQL versioning policy](https://www.postgresql.org/support/versioning/).

## Monorepo checks

Run commands from the repository root. `make test` includes backend unit tests
and frontend checks. `make check` adds Go vet, formatting, race detection,
and the production/demo import boundary. Direct Go commands target the module:
`go -C backend test ./...`. The E2E Go module lives at `tests/e2e/`; its
integration tests require the explicit disposable DSNs supplied by
`scripts/integration-test.sh`, which creates a temporary Go workspace.

`make docker-smoke` builds both images (`backend/Dockerfile`,
`web/Dockerfile`), starts an isolated Compose stack on random loopback ports,
and verifies backend health and readiness (migrations), that the backend
serves no UI, the frontend's UI, hashed assets, SPA routing, security and
cache headers, the `/api/` proxy (401 without a session, login with an
`HttpOnly`/`SameSite=Strict` cookie, an authenticated call, and a refused
cross-origin mutation), the CLI and lab runner, and non-root users in both
images, then removes only that stack's containers and volumes.
`make e2e` installs the separate Playwright dependencies in `tests/e2e/` and
runs its browser flows against another isolated stack.

## Investigation workspace checks

`backend/internal/domain/workspace` adds unit and integration coverage for safe error messages,
session-owned cancellation, policy isolation/revocation, bounded concurrency, cursor
query grammar, read-only rejection, keyset pagination, parameterized filtering,
NULL/empty values, serialized JSON/protocol limits, catalogs, and generic plans
(`TestExplainGenericPlan`: one statement only, refused before PostgreSQL 16, planned
without deleting a row on 16+). The integration harness
includes this package on both default PostgreSQL versions.

The browser suite covers catalog and row browsing through the data explorer
(main path above). The SQL capability is API-only and has no UI; it is covered
by the backend integration tests. The dated
[workspace verification report](workspace-verification.md) describes the earlier
UI and its results.
