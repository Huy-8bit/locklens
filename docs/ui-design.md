# UI guide

The web UI answers one question first: **what is this PostgreSQL instance
doing right now, and how did it get here?** Everything else (queries, data,
replication, alerts) is one click away for the same instance.

## Layout

- **Sidebar**: the instance selector (switching keeps the current page and time
  range), the pages of that instance, then **All instances** and **Settings**.
  Below 1024 px the sidebar becomes a menu.
- **Top bar**: the instance's state, address, PostgreSQL version and role, how
  long ago it was last sampled, the live event-stream status, the theme
  switch, and sign-out.
- **Time range** (history charts only): 15 minutes (default), 1 hour, 6 hours,
  24 hours, 7 days. It is kept in the URL (`?range=`), so a view can be shared.

## Pages

| Page | Answers | Main sources |
| --- | --- | --- |
| **Overview** | Is the instance healthy, how busy is it, what kind of work is it doing? Twelve current values (connections, running queries, queries/s, transactions/s, cache hit ratio, size, blocked sessions, oldest transaction, rows read/written per second, temp files, replica lag), queries by type over time, transactions, connections by state, rows read and written, execution time, cache hit ratio, WAL, temp files; the query mix, the longest-running queries now, the most expensive queries, alerts, and collector status | latest samples, metric history, `pg_stat_statements` |
| **Live activity** | Who is connected and doing what right now? Counts by state, the blocking tree ("who is blocking whom"), and sessions that run a query, hold a transaction open, or take part in blocking, with their SQL; a session panel shows everything about one backend | latest activity sample (`pg_stat_activity`, `pg_blocking_pids()`, `pg_locks`) |
| **Queries** | How many queries run, of which type, how fast? Totals and coverage, executions/time/rows per second **by statement type** over time, a breakdown table per type (click to filter), and every statement with its normalised SQL, calls, rate, total and average time, rows; optional comparison with the previous period. A statement page shows the formatted SQL, its rate and average time over time, and the comparison | `pg_stat_statements` deltas |
| **Data explorer** | What is in the database? Databases with sizes, the largest tables (statistics), and, with browsing access, schemas → tables → **Data** (rows, paged, sortable, filterable; NULL and empty strings stay distinct; a cell opens in full, JSON pretty-printed), **Columns**, **Indexes** (size, scans), **Constraints** | table statistics; catalog and rows through the separate browsing role |
| **Replication** | Is replication keeping up? Role, standbys with byte and time lag, slots and retained WAL, standby receive/replay state, lag and WAL charts, archiving | replication collector |
| **Alerts** | What has gone wrong? Incidents (open or all) and current findings with evidence, possible explanations, next checks, limitations; rules that could not be evaluated are listed separately | rules and incident recorder |
| **Incident** | The recorded evidence: timeline (pre-trigger samples, trigger, stored blocking graphs, resolution), JSON and standalone HTML export, acknowledgement as a note | incident store |
| **All instances** | Every monitored server with state, connections, queries/s, transactions/s, blocked sessions, cache hit ratio, size, open incidents | latest samples |
| **Settings** | Instances (add, edit, test connection, delete), alert thresholds, monitoring store size | configuration |

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
- **Rates by type** divide each bucket's executions by the collection time the
  bucket actually covers. A covered bucket with no statements of a type is
  zero; a bucket without collections is a gap.
- **One axis per chart.** Measures with different units (calls per second and
  average milliseconds) are separate charts.
- **Colors** follow the statement type or the series, never their rank:
  SELECT is always blue, INSERT orange, UPDATE green, DELETE yellow, MERGE
  pink, COPY dark green, DDL violet, and the rare types share a neutral gray
  ("Other"). The palette is a validated colorblind-safe categorical order in
  both themes. Health colors (green, amber, red) are reserved for state and
  always come with an icon and a word.
- Charts with two or more series have a legend with each series' latest value;
  hovering shows a crosshair tooltip with exact values. Breakdown tables next
  to the charts carry the same numbers as text.
- Collection gaps recorded by the server are shaded.

## Accessibility and states

- Every page has loading, empty, error, and partial states; nothing shows
  placeholder data.
- Controls are native elements with labels; tables have captions; dialogs and
  panels close with Escape and return focus.
- Light and dark themes are chosen values, not an automatic inversion; the
  choice is remembered per browser. Motion is disabled under
  `prefers-reduced-motion`.
- Pages are checked with axe-core in unit tests and in the browser E2E suite,
  and screenshot-checked at 1440 px and 390 px in both themes.

## Privacy in the UI

- SQL text is shown because the instance collects it (the default). It can be
  switched off per instance in **Settings**; pages then show query IDs.
- Table rows are visible only after an administrator configures a separate
  SELECT-only role and enables row browsing for that instance (with a
  confirmation). There is no free-form SQL editor in the UI.
- Connection strings are write-only fields and are never displayed.
