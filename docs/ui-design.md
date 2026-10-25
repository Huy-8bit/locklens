# UI guide

The web UI answers one question first: **what are my PostgreSQL databases
doing right now, and how did they get here?** The Overview shows a whole
environment on one screen; every card leads to a page with the detail.

## Layout

- **Sidebar**: Overview, Databases, Topology, Queries, Sessions, Blocking,
  Explorer, SQL Editor, Incidents (with the number of open incidents),
  Connections, Settings. At the bottom, **System status**: the monitoring
  store, the collectors, alerts, and the store writer, from `GET /api/v1/system`.
  Below 1024 px the sidebar becomes a menu.
- **Top bar**: search (⌘K / Ctrl+K) over pages, databases, open incidents, and
  statement text; the **environment** selector; the **time range**; the
  **Live** switch; notifications (open incidents); and the account menu with the
  signed-in user and their role, the theme switch, Account, Settings
  (administrators), and sign-out.
- **Roles**: the sidebar and pages show only what the role can use. Viewers do
  not see the SQL Editor or Settings, and see table structure but not rows;
  operators see everything except Settings. The server enforces the same rules. Below
  768 px the environment and time range move to a bar under the top bar.
- **Environment**: every instance has an environment label (`production`,
  `staging`, `lab`, … default `default`). Fleet pages show the instances of the
  selected environment, or all of them.
- **Time range**: every history chart and table follows one range, chosen in
  the top bar like in Grafana: quick ranges (15 minutes, 1 hour (default), 6
  hours, 24 hours, 7 days) or an absolute window (from–to, at most 31 days),
  buttons to move the window back or forward by half its length and to zoom
  out (twice as long), and **Back** to return to the range before the last
  zoom or move. An absolute window is not remembered across reloads.
- **Live**: when on, current-state views refresh after every collection (event
  stream) and on a short interval; when off, nothing refreshes until it is
  switched back on. Environment, range, and Live are remembered per browser.

## Overview

| Row | Cards |
| --- | --- |
| KPIs | **Databases monitored** (and their states), **Active connections** (sum of used connections, trend over the range, sum of `max_connections`), **Query throughput** (statement executions per second, trend, mean latency), **Replication health** (standbys in sync / observed standbys), **Slow queries** (statements with a mean execution time above 1 s in the range), **Incidents** (open, by severity). Each card opens its page. |
| Activity | **Read / write operations** (rows read vs. written per second, or commits vs. rollbacks), **Query throughput & latency** (executions per second and mean latency as two stacked panes on one time axis), **System resources** (CPU, memory, fullest filesystem from node_exporter, deduplicated per exporter; disk I/O as text) |
| State | **Database topology** (primaries and the standbys streaming from them, with replay lag and its history), **Top queries** (ranked by total time, calls, mean latency, or rows, across instances), **Active sessions** (running a query or holding a transaction) |
| Investigation | **Blocking transactions** (blocker → waiting sessions), **Database explorer** and **SQL editor** for one instance (selectable) |

Trend labels compare the first and last known value of the range. A trend is
green or red only where a direction is good or bad (none of the KPIs above
claims that; they are shown in neutral gray); the arrow and the number carry
the meaning without color.

## Pages

| Page | Answers | Main sources |
| --- | --- | --- |
| **Databases** | Every monitored server of the environment with state, connections, queries/s, transactions/s, blocked sessions, cache hit ratio, size, open incidents | latest samples |
| **Database** (`/databases/:id`) | One instance, in tabs: **Overview** (twelve current values, queries by type, transactions, connections by state, rows, execution time, cache, WAL, temp files, query mix, longest-running queries, most expensive queries, alerts, collectors), **Queries** (by type, every statement with SQL), **Activity** (sessions and the blocking tree), **Replication** (role, standbys, slots, WAL, archiving), **Alerts** (incidents and findings) | latest samples, metric history, `pg_stat_statements` |
| **Topology** | How replication is laid out and whether it keeps up: the replication graph, streaming standbys with byte and time lag and a health label, slots and retained WAL, instances outside any observed pair | replication collector |
| **Queries** | The most expensive statements across instances (top 50 by total time, calls, mean latency, or rows), filterable by type and SQL text. A statement page (`/queries/:target/:id`) shows the formatted SQL, rate and mean time over time, and the comparison with the previous period | `pg_stat_statements` deltas |
| **Sessions** | Who is connected and doing what, across instances: counts by state and every client session that runs a query, holds a transaction, or waits, with its SQL; a panel shows everything about one backend | latest activity samples |
| **Blocking** | Who waits on whose lock: blocker → waiter cards, the full tree per instance with lock modes and objects, long-running transactions | `pg_blocking_pids()`, `pg_locks` |
| **Explorer** | What is in a database: schema objects (tables, views, functions, types) and, for a table or view, **Data preview** (rows, paged, sortable, filterable; NULL and empty strings stay distinct; a cell opens in full, JSON pretty-printed), **Schema** (columns, constraints), **Indexes** (size, scans), **Triggers**. Without browsing access: databases with sizes and the largest tables from statistics | catalog and rows through the separate browsing role |
| **SQL Editor** | Read-only investigation: one statement at a time, results (at most 500 rows or 1 MiB), the **execution plan** as a graph from `EXPLAIN (FORMAT JSON)` (no ANALYZE: the statement is planned, not run), and messages with PostgreSQL's error code and position | separate browsing role |
| **Incidents** | Every recorded incident (open or all) with instance, duration, and acknowledgement. An incident page explains what happened in plain words with the key numbers, shows the statement involved (SQL, time per call and calls around the incident) or the blocking graph, the sessions active when it opened with their SQL, the top statements by execution time, the instance's signals around it, and a timeline with repeated samples collapsed; JSON and standalone HTML export | incident store |
| **Connections** | Connection usage per instance (used / max, reserved), client sessions by state over time, and who is connected: sessions grouped by database, user, application, client address, and state, with the oldest in each group | `pg_stat_activity` (grouped, LockLens's own connections excluded) |
| **Settings** (administrators) | Instances (add, edit, environment, test connection, delete), **Users** (add, change role, reset password, disable, delete), alert thresholds, incident import, monitoring store size | configuration |
| **Add an instance** | Paste one connection string, check it, read the plan (use as is, reuse LockLens's roles, or create them for a superuser, with the statements shown), choose row browsing and the SQL editor, add. An advanced form sets interval, pool size, and node_exporter | `targets/inspect`, `targets/quick` |
| **Account** | Your user name and role, what the role allows, and a password change (except for the environment administrator) | session |

## Statement types

Every statement is classified from its leading SQL keyword when it is
collected (a `WITH` query by its main statement): SELECT, INSERT, UPDATE,
DELETE, MERGE, COPY, DDL (CREATE, ALTER, DROP, TRUNCATE, GRANT, …),
transaction control (BEGIN, COMMIT, ROLLBACK, SAVEPOINT, …), maintenance
(VACUUM, ANALYZE, REINDEX, …), utility (SET, SHOW, EXPLAIN, DO, CALL, …), and
other. It is a lexical heuristic for grouping, not a parser: a `DO` block that
deletes rows counts as utility. Classification needs the statement text at
collection time; the text itself is stored only when SQL text collection is on.

## Reading the charts

- **Unknown is not zero.** A missing sample, a counter reset, or a server
  restart breaks the line; it is never drawn as zero. Values that cannot be
  computed show "—".
- **Across instances**, rates, counts, and sizes are summed; ratios are
  averaged over the instances that report them; lag and peaks keep the
  maximum. A bucket is unknown when no instance has a value in it.
- **Mean latency** is execution time per second divided by executions per
  second in the same bucket (an execution-weighted mean), unknown when idle.
- **Rates by type** divide each bucket's executions by the collection time the
  bucket actually covers. A covered bucket with no statements of a type is
  zero; a bucket without collections is a gap.
- **One axis per chart.** Measures with different units (executions per second
  and milliseconds) are separate panes that share the time axis.
- **Colors** follow the statement type or the series, never their rank:
  SELECT is always blue, INSERT orange, UPDATE green, DELETE yellow, MERGE
  pink, COPY dark green, DDL violet, and the rare types share a neutral gray
  ("Other"). The palette is a validated colorblind-safe categorical order in
  both themes. Health colors (green, amber, red) are reserved for state and
  always come with an icon or a word.
- **Replication health**: a time lag under 1 s is healthy and under 30 s a
  warning; without a time lag (an idle primary reports none), under 16 MiB of
  WAL not yet replayed is healthy and under 256 MiB a warning; a standby that is
  not streaming is critical. These are display thresholds, not alert rules.
- Charts with two or more series have a legend with each series' latest value.
  Click an entry to show only that series (click again to show all);
  Ctrl/⌘/Shift-click toggles one series.
- Hovering shows a crosshair and a tooltip with exact values, sorted from the
  largest, with a total for stacked charts. The crosshair is synchronised: all
  charts of the page show the same moment.
- **Zoom**: drag across a chart to zoom every chart and table of the page into
  that window (at least one minute); the data is fetched again at the finer
  resolution the window allows (never finer than the collection interval).
  Double-click a chart, or use the top bar's zoom-out button, to zoom out.
- **View larger** (the icon at a chart's top right) opens the chart in a large
  dialog with the same interactions.
- Collection gaps recorded by the server are shaded.

## Accessibility and states

- Every page has loading, empty, error, and partial states; nothing shows
  placeholder data. Values the lab cannot provide (for example disk I/O when
  node_exporter has no disk statistics) say "unavailable".
- Controls are native elements with labels; tables have captions; dialogs,
  panels, and the search palette close with Escape and return focus.
- Light and dark themes are chosen values, not an automatic inversion; the
  choice is remembered per browser. Motion is disabled under
  `prefers-reduced-motion`.
- Pages are checked with axe-core (including color contrast) in unit tests and
  in the browser E2E suite, and screenshot-checked at 1600 px and 390 px in
  both themes.

## Privacy in the UI

- SQL text is shown because the instance collects it (the default). It can be
  switched off per instance in **Settings**; pages then show query IDs.
- Table rows and the SQL editor are available only to operators and
  administrators, and only after an administrator enables **Browse table rows**
  or **Read-only SQL editor** for that instance (when adding it, or with a
  confirmation later). They never use an administrative role. The
  editor runs one statement inside a read-only transaction that is always
  rolled back, with row, byte, and time limits; statements and results are never
  stored. It cannot kill sessions, cancel other queries, or change the server.
- SQL drafts are kept in memory only (they survive navigation, not a reload).
- Connection strings are write-only fields and are never displayed.
