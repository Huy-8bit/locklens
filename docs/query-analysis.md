# Query workload analysis

The **Queries** page and `GET /api/v1/targets/{id}/queries` show which
statements consume execution time on a target, how that changed between two
equal windows, and how complete the underlying data is. The data comes from
the `pg_stat_statements` extension.

## Requirements

- `shared_preload_libraries` includes `pg_stat_statements` (server restart
  required), and
- `CREATE EXTENSION pg_stat_statements;` was run in **the database LockLens
  connects to**, and
- the monitoring role can read it. With `pg_read_all_stats` it sees all
  users' statements; without it, other users' rows have no `queryid` and are
  counted as hidden (`hidden_entries_max` in the coverage).

When any condition is missing, the statements collector is disabled with the
exact reason (for example "extension pg_stat_statements is not installed in
the database LockLens connects to"), the page says so, and the workload rules
report "insufficient data". Nothing is estimated.

## Query identity

`pg_stat_statements` keys entries by database, user, top-level flag, and a
normalised `queryid`. LockLens identifies a query by:

```
(target, server major version, dbid, userid, toplevel, queryid)
```

The server major version is part of the key because `queryid` values are not
stable across major versions. The same SQL run by two users, or in two
databases, is two identities. Query IDs are 64-bit signed integers; the API
returns them as strings so JavaScript does not round them.

## Deltas between collections

The collector reads cumulative counters every 15 s
(`LOCKLENS_STATEMENTS_INTERVAL`; texts only for statements not seen before, see
[collectors.md](collectors.md#statement-texts)) and
stores **per-collection deltas** for entries that changed: calls, total
execution time (the mean is derived), rows, shared blocks hit/read, temp blocks written, and WAL
bytes. Rules for computing deltas:

| Situation | Treatment |
| --- | --- |
| First collection after LockLens starts | Baseline only, no deltas |
| `pg_stat_statements_reset()` since the previous collection (detected from `pg_stat_statements_info.stats_reset`) | Current values are the deltas; the collection is marked as a reset |
| New entry | Created after the previous collection, so its values are deltas |
| Counter decreased | The entry was evicted and re-created; current values are deltas |
| Evictions (`pg_stat_statements_info.dealloc` increased) | Evicted entries' counts in that interval are lost; the count is reported as `evicted_entries` |
| More than 2,000 changed entries in one collection | The 2,000 with the largest execution-time deltas are stored; the rest are counted as truncated |

Window totals are sums of stored deltas, so they stay correct across resets
and evictions except for the reported losses.

## Coverage

Every workload response includes coverage for the window:

- `window_seconds` and `covered_seconds`: how much of the window is backed by
  collections (gaps, restarts, and the first baseline reduce it);
- `collections`, `resets`, `evicted_entries`, `hidden_entries_max`.

The UI shows coverage next to the table. A short or partially covered window
is labelled as such; totals are never extrapolated.

## Window comparison

With **Compare with previous window** (`compare=true`), each identity's totals
in the selected window are compared with the previous window of equal length.
For each query:

- `calls_ratio` = calls(current) / calls(previous)
- `load_ratio` = total time(current) / total time(previous)
- `mean_ratio` = mean time(current) / mean time(previous)

Flags, with the thresholds from **Settings → Alert rules** (defaults; demo
mode in parentheses):

| Flag | Condition |
| --- | --- |
| too few calls | either window has fewer than `regression_min_calls` = 50 (30) calls; no other flag is set |
| mean regression | `mean_ratio` ≥ `regression_mean_ratio` = 2 **and** the mean grew by ≥ `regression_min_delta_ms` = 5 ms |
| volume increase | `calls_ratio` ≥ `volume_calls_ratio` = 3 **and** current calls ≥ `volume_min_calls` = 500 (200) |
| load increase | `load_ratio` ≥ `volume_calls_ratio` |

A regression (each call is slower) is shown separately from a volume increase
(more calls, each as fast as before): they have different causes.

## Rules built on the workload

Six statement rules (`queries.mean_regression` is an incident trigger)
compare the current `workload_window_seconds` window (900 s; 120 s in demo
mode) with each statement's **baseline**, its own history summarised by the
median and MAD per hour of day, and fall back to the previous equal window
while the baseline is learning. They require at least `workload_min_coverage`
= 80% coverage of the window, otherwise they report insufficient data with
the measured coverage. At most five queries per rule are reported, strongest
first. `queries.long_running` watches statements while they run. See
[query-intelligence.md](query-intelligence.md) and [rules.md](rules.md).

## Limitations

- Only window means are available. `pg_stat_statements` keeps no
  per-execution samples, so percentiles and outliers cannot be computed.
- A slower mean does not identify the cause. LockLens never runs `EXPLAIN`;
  plan changes, lock waits, I/O, and larger result sets are listed as
  possibilities to check.
- Normalised statements still contain structure and may contain literals in
  some cases (for example in utility statements).
- `pg_stat_statements.max` limits how many entries the server keeps;
  evictions lose data and are reported.
- Statements of LockLens' own monitoring user are hidden by default
  (`include_monitoring=true` shows them). They are real load, but rarely the
  subject of an investigation.

## Statement types

Each identity carries a command class derived from its normalised text at
collection time: `select`, `insert`, `update`, `delete`, `merge`, `copy`,
`ddl` (CREATE, ALTER, DROP, TRUNCATE, GRANT, REVOKE, COMMENT, …),
`transaction` (BEGIN, COMMIT, ROLLBACK, SAVEPOINT, RELEASE, PREPARE
TRANSACTION), `maintenance` (VACUUM, ANALYZE, REINDEX, CLUSTER, CHECKPOINT),
`utility` (SET, SHOW, RESET, EXPLAIN, DO, CALL, LOCK, cursor and LISTEN/NOTIFY
commands, …), and `other`. The leading keyword decides, after comments; a
`WITH` query is classified by the first data-modifying or SELECT keyword at
parenthesis depth zero after its common table expressions, and `PREPARE name
AS …` by the prepared statement. This is a lexical heuristic for grouping,
not a SQL parser (a `DO` block that deletes rows is `utility`).

The collector always reads the normalised text to classify it; the text is
then kept only when the target collects SQL text. Identities seen before this
classification existed are `other` until they are seen again.
`GET /api/v1/targets/{id}/queries/commands` returns totals and a time series
per class; `command=` filters the statement list. Rates divide each bucket's
sum by the statement-collection time it covers, so a covered bucket without
statements is zero and an uncovered one is unknown.

## SQL text privacy

SQL text is **collected by default** for every target, so the Queries and
Sessions pages can show it:

- The normalised statement text is stored once per identity, and live session
  queries are kept in activity snapshots.
- It can be switched off per target (**Settings → Instances → Edit → Collect SQL
  text**). Then the text is not stored (it is still read briefly to classify
  each statement and then dropped), session queries are not collected, and
  the target's stored statement texts are deleted.
- The API returns text when the target collects it unless the request passes
  `include_text=false`; exports include it only with an explicit option.
- SQL text never appears in Prometheus labels, logs, or error messages.

Normalised statements replace literal values with `$1`, `$2`, …, but running
session queries and some statements (utility commands, literals in
identifiers or comments) can contain data. No redaction can guarantee that
literals are removed, so treat collected text as sensitive data.
