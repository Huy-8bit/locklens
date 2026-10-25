# Implementation plan

Live checklist. Status markers:

- `[x]` done: implemented, verified, and documented
- `[~]` in progress
- `[ ]` pending
- `[!]` blocked or not verified (reason given)

## Phase 1: CLI MVP (complete)

The read-only CLI (`doctor`, `inspect`, `watch`, `snapshot save/show`,
`version`), the blocking graph, snapshot schema v1, the isolated integration
harness, and the QA round are complete. Evidence: [test-report.md](test-report.md).

- [x] Edges only from `pg_blocking_pids`; parallel workers folded into leaders; PID 0 prepared transactions; cycle-safe graph
- [x] Transaction, query, and lock wait ages kept distinct; lock wait only from `pg_locks.waitstart`
- [x] Read-only sessions, own-PID exclusion, credential redaction, opt-in query text, terminal sanitising
- [x] Integration suite on PostgreSQL 14.24, 15.19, 16.15, 17.11, 18.6

## Phase 2: monitoring and incident investigation application

### Architecture decisions

- One Go binary: the existing CLI plus `locklens server` (REST API, SSE,
  background collectors, embedded React UI). No separate services.
- Monitoring store: a dedicated PostgreSQL 14+ database (`LOCKLENS_STORE_DSN`),
  versioned SQL migrations embedded in the binary. Never the monitored database.
- Targets: DSNs encrypted with AES-256-GCM using `LOCKLENS_MASTER_KEY` (env only).
  Collectors use small per-target pools, read-only sessions, own-PID exclusion.
- Scheduler: one worker goroutine per target running its collectors
  sequentially (no overlap per target), a global semaphore bounding concurrent
  collections, per-collector intervals (light: activity; medium: counters,
  statements, replication; heavy: table and database sizes).
- Counters stored as derived rates/deltas computed at ingest against the
  previous sample, with explicit handling of restarts, stats resets, negative
  deltas, and gaps (unknown is stored as NULL, never 0).
- Query identity: (target, server major version, database, user, toplevel,
  queryid). Text stored once per identity and only when the target opts in.
- Rules: deterministic Go functions with stable IDs and versions; findings
  carry evidence, explanation, next checks, and limitations.
- Incidents: per-target recorder with a bounded in-memory pre-trigger buffer,
  persistence thresholds, cooldown/regrouping, duration and size limits,
  restart recovery from the store.
- Single admin, bcrypt password hash, server-side sessions in the store
  (HttpOnly SameSite=Strict cookie), CSRF token header plus Origin check.
- Frontend: React + TypeScript + Vite, React Router, TanStack Query,
  Apache ECharts, CodeMirror, Tailwind CSS; built assets embedded in the Go binary.

### Milestones

1. [x] Audit, architecture, acceptance checklist (this file)
2. [x] Storage, migrations, configuration, authentication, target management
3. [x] Collectors, health overview API, React shell
4. [x] Query workload analysis and query UI
5. [x] Blocking investigation and session UI
6. [x] Vacuum/table health and replication/WAL
7. [x] Incident recorder, rules, incident UI
8. [x] Host metrics, exports (JSON/HTML), Prometheus metrics
9. [x] Docker incident lab (store, primary, standby, LockLens image, lab runner, nine scenarios)
10. [x] Browser E2E, performance checks, documentation, final verification

Evidence for every item: [test-report.md](test-report.md).

### Acceptance checklist

Backend and storage
- [x] Versioned migrations with advisory lock; idempotent startup (`TestMigrationsConcurrentAndIdempotent`)
- [x] Retention cleanup in batches; incident retention separate from raw history (`TestRetentionInBatches`)
- [x] Bounded write buffering on store outage with drop accounting and recorded gaps (writer unit tests, `TestOutageAndRecovery`)
- [x] Liveness, readiness, request IDs, body/range/page limits, structured logs (`backend/internal/api` tests, `TestAuthAndSecurityHeaders`)

Security
- [x] Localhost bind by default; admin bootstrap from environment
- [x] bcrypt hashes; server-side sessions; CSRF protection; same-origin by default (`backend/internal/auth` tests, server and E2E tests)
- [x] DSNs encrypted at rest; responses, logs, exports never contain DSNs or passwords (credential checks in every server-test response and E2E export)
- [x] Query text off by default; per-target opt-in; never in metric labels (`TestRestrictedRoleAndQueryTextOptIn`, `TestPgStatStatementsAvailable`, obs tests)
- [x] Import size limits and validation; HTML escaping; terminal sanitising (export tests, E2E import)

Targets
- [x] Add, edit, disable, delete (soft delete with explicit purge), test connection
- [x] Version and capability detection; per-target interval and connection limit
- [x] Status: healthy, degraded, unreachable, disabled (documented criteria); freshness
- [x] Failures isolated per target and per collector (healthy and unreachable targets side by side in `TestTargetLifecycleBlockingIncidentAndExport`)

Collectors and views
- [x] Health metrics, version-aware checkpointer/bgwriter, `pg_stat_io` (16+), database sizes on the slow interval, long transactions
- [x] Query workload: windows, sorting, filtering, pagination, drill-down, equal-window comparison, flags with minimum calls
- [x] Blocking: graph/tree, sessions, node details, roots, counts, historical snapshots
- [x] Table/vacuum: tuple estimates, vacuum/analyze times, progress, sizes, XID age
- [x] Replication/WAL: role, replicas and LSN lags, time lag fields, slots and retained WAL, archiver deltas, WAL trends
- [x] Host metrics from an optional node_exporter (timeouts, size limit, "Unavailable" when absent)

Incidents and rules
- [x] Thirteen rules with IDs, versions, evidence, limitations; missing data never passes
- [x] Five incident triggers; pre/during/post capture; cooldown and grouping; limits; acknowledge as metadata only (`backend/internal/incidents` integration tests)
- [x] Restart recovery of incident metadata (`TestRestartRecovery`, server restart test)

CLI, exports, metrics
- [x] Existing CLI unchanged and passing; `server`, `migrate`, `admin hash-password`, `incident export`, `incident report`
- [x] Incident JSON (versioned) and standalone HTML (no CDN, escaped, no credentials), opened offline in E2E
- [x] `/metrics` with bounded labels

UI
- [x] Twelve pages; dark/light; responsive; keyboard and labels; loading/empty/partial/error/disconnected states; time range in URL; timezone and freshness; confirmations; reduced motion (screenshots in light, dark, and mobile widths; axe-core in unit and E2E tests)

Lab and tests
- [x] One-command Docker demo with healthchecks and isolated volumes (`make up`)
- [x] Nine lab scenarios with start/stop/status, bounded duration, cleanup
- [x] Go unit, race, integration (store, collectors, recorder, primary/standby, outage, restart) on PostgreSQL 14.24, 15.19, 16.15, 17.11, 18.6
- [x] Frontend typecheck, lint, unit, build, accessibility checks
- [x] Playwright E2E against the real stack, offline report check
- [x] Performance smoke checks (collection duration, connections, memory, storage growth, concurrency limit)

Not done in this phase (see [limitations.md](limitations.md)): CI has not run on
GitHub (nothing pushed); no multi-user access control; no master key rotation
command; TLS and a real node_exporter were not exercised.

## Pending ideas (not planned)

- Display-only enrichment of the lock a blocker holds
- `snapshot diff`
- Release binaries
- Code-splitting the UI bundle (about 800 KB minified, 233 KB gzip)
- Master key rotation command; multiple users and roles


## Investigation workspace audit (2026-10-24)

This checklist tracks complete paths through collection, storage, APIs, UI and tests.
Existing uncommitted chart/lab changes are retained. No commit or push is authorized.

| Area | Audit category | Work and verification |
| --- | --- | --- |
| Monorepo, authentication, target management | Existing and previously verified | Preserve existing root commands and encrypted collector credentials |
| Charts, global timeline, gaps, blocking incidents | Existing and previously verified | Preserve interaction; add available-history bounds |
| Fleet dashboard | Existing, needs improvement | Summary charts, comparisons, ranked pressure, findings and topology |
| Logical tuple activity | Missing | Extend cumulative counters and elapsed-time rates through dashboard |
| Database catalogs / actual table data | Missing | Dedicated policy/credentials, bounded on-demand queries and grid |
| SQL workspace | Missing | Read-only connection, cursor grammar, limits, owned cancellation, plans |
| Workspace policy / audit | Missing | Default-deny capabilities, encrypted DSN, safe metadata-only audit |
| Live activity / application map | Existing samples, needs presentation | Explicit sampled semantics and filters, no invented throughput |
| Replication topology | Missing | Explicit mapping provenance plus separate streaming evidence |
| Relational lab | Missing | Customers, products, orders, items, JSONB and investigation role |
| Physical host metrics | Unavailable without exporter | Never infer CPU or disk from PostgreSQL execution time or buffers |
| Query calls by statement type / application throughput | Unavailable from current aggregate source | Do not guess classification or per-application execution rates |
| Browser / integration coverage | Needs extension | Real database flows and restricted / oversized results |

Verification status and remaining limits are recorded in `workspace-verification.md` after execution.

### Completed implementation and evidence

- [x] Fleet comparisons, source coverage, target rankings, findings, topology and shared-range charts.
- [x] Logical tuple counters → interval-aware storage → metrics API → target panels and real lab evidence.
- [x] Catalogs, bounded data grid, safe filtering/sorting, keyset/offset pages and page exports.
- [x] Dedicated encrypted investigation credentials; default-deny capabilities and safe audit metadata.
- [x] SQL editor, read-only execution, explicit selection, owned cancellation, limits, history policy and EXPLAIN tree.
- [x] Live activity filters and application/session grouping with sampling limitations.
- [x] Explicit replication mapping/provenance, corroborated streaming state, node details and viewport controls.
- [x] Available-history fitting, source/aggregation information and fullscreen historical charts.
- [x] Relational local lab, CRUD verification, observed tuple/WAL/statement activity and resolved blocking incidents.
- [x] Backend/frontend/integration/Docker/browser checks and screenshots; see `workspace-verification.md`.
- [x] Diff/ignore review; unrelated `SECURITY.md` preserved; no commit or push.

Unavailable and deliberately unimplemented areas are enumerated in the verification
report. These include absent exporters/version-specific sources, per-application
throughput, multi-user RBAC, automatic cluster discovery and metadata navigation beyond
the bounded catalog cap. They are not represented as working empty panels.

## UI rebuild (2026-10-25)

Goal: a UI that shows what each database is doing now, its metrics, how many
queries of each type run and how they behave, and its data.

- [x] Backend: statement command classification at collection time
  (`observe.ClassifyCommand`, migration `0004`), `GET /queries/commands` with
  per-bucket coverage, `command` filter on the statement list
- [x] SQL text collected by default (per-target switch kept); API returns it
  unless `include_text=false`
- [x] Lab: separate `locklens_reader` role, demo `shop` schema, bootstrap of
  data-explorer access, `shop` mixed-workload scenario
- [x] Frontend rewritten: Overview, Live activity, Queries (by type), Data
  explorer (structure and rows, no SQL editor), Replication, Alerts, Incident,
  All instances, Settings (instances, thresholds, incident import, store size)
- [x] Validated categorical palette, one axis per chart, unknown never zero
- [x] Unit tests (classifier, command series, pages with axe), make check
- [x] Screenshot review: 14 pages × light/dark × 1440/390 px, no console
  errors, failed requests, or overflow
- [x] Integration harness (14.24, 18.6) and browser E2E for the new UI (see
  [test-report.md](test-report.md#ui-rebuild-2026-10-25))
- [x] axe-core with color contrast on every page in both themes
