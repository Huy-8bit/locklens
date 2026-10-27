# Query performance intelligence

LockLens compares every active statement with **its own history**, detects
statements that are slow **while they are still running**, ranks what it
finds by **impact**, and correlates the evidence around an anomaly into
**ranked hypotheses**. Everything is deterministic: the same data always
gives the same result, every number traces back to a measurement, and a
hypothesis is never presented as a confirmed cause.

It reuses the existing collectors and stores. It adds no infrastructure
(no Kafka, Redis, or ClickHouse), runs no DDL or DML on targets, and never
runs `EXPLAIN ANALYZE`.

- [Data sources](#data-sources)
- [Baselines](#baselines)
- [Anomaly rules](#anomaly-rules)
- [Slow running queries](#slow-running-queries)
- [Confidence](#confidence)
- [Impact score](#impact-score)
- [Correlation and hypotheses](#correlation-and-hypotheses)
- [Execution plans](#execution-plans)
- [Percentiles](#percentiles)
- [Where to see it](#where-to-see-it)
- [Limitations](#limitations)

## Data sources

| Source | Collector | Used for |
| --- | --- | --- |
| `pg_stat_statements` deltas (`query_samples`, `statement_collections`) | statements | Calls, execution time, rows, blocks, temp per statement and period; coverage and resets |
| `pg_stat_activity` (`query_start`, `query_id`, wait events) | activity | Statements running now, their age, what they wait on, who blocks them |
| Active session sampling (`load_samples`) | load | What a statement (by `query_id`) and the instance waited on, before and during an anomaly |
| Other rule findings | rules | Blocking, long transactions, vacuum, connections, temp files around the anomaly |
| Saved plans (`query_plans`) | on request only | Plan shape and cost over time |

`pg_stat_activity.query_id` needs PostgreSQL 14+ and `compute_query_id`
(`auto` turns it on when `pg_stat_statements` is loaded).

## Baselines

Baselines are built from **five-minute rollups** (`query_rollups`,
`statement_coverage`), maintained incrementally from raw statement samples
every 5 minutes (every minute in demo mode). Rollups are kept for
`LOCKLENS_BASELINE_RETENTION` (default 15 days), longer than raw history, so
a baseline spans days without scanning raw samples.

For each of the (at most 300) statements with the most execution time in the
last two hours, LockLens aggregates rollups into **points** of
`baseline_point_seconds` (1 hour; 5 minutes in demo) over the last
`baseline_days` (7). The two most recent workload windows are left out, so
the period a rule evaluates is never part of what it is compared with.

Per point it computes:

| Measure | Definition | Needs |
| --- | --- | --- |
| `calls_per_s` | calls / covered seconds | coverage |
| `load_ms_per_s` | execution time / covered seconds | coverage |
| `mean_ms` | execution time / calls | ≥ `baseline_min_calls_per_point` calls |
| `rows_per_call`, `reads_per_call`, `temp_per_call` | per call | same |
| `cache_hit_ratio` | hits / (hits + reads) | same |

Robust statistics: the **median** and the **median absolute deviation**
(MAD, × 1.4826 to be comparable with a standard deviation). A few extreme
points barely move them. A constant history (MAD = 0) uses a floor of 5% of
the median, so tiny changes are not "infinitely unusual".

**Gaps and resets.** A point with no collection coverage is *unknown* and is
left out; a covered point in which the statement did not appear counts as
zero calls (quiet periods are real). A point with a detected
`pg_stat_statements` reset is skipped.

**Time of day.** When an hour of the day has at least 5 points, that hour's
own profile is used ("same hour of day"); otherwise all hours are used
("all hours"). A nightly batch that is always slow at 02:00 is therefore not
an anomaly at 02:00.

**Warm-up.** A baseline is used only after `baseline_warmup_points` (24; 6
in demo) covered points. Until then the rules compare the current window
with the previous one, as before, and say so in their evidence and
confidence.

## Anomaly rules

All run on the current `workload_window_seconds` window (900 s; 120 s in
demo) and need `workload_min_coverage` (80%) of it collected; otherwise they
report `insufficient_data`. A value is anomalous when it is at least
`anomaly_sensitivity` (4) robust deviations above the baseline median **and**
beyond the rule's ratio. At most five findings per rule, strongest first.

| Rule | Fires when | Severity | Without a warm baseline |
| --- | --- | --- | --- |
| `queries.mean_regression` (trigger) | Time per call ≥ `regression_mean_ratio` (2)× usual, +≥ `regression_min_delta_ms` (5 ms), ≥ `regression_min_calls` calls | warning; critical at ≥ `regression_critical_ratio` (5)× | previous window |
| `queries.call_volume_increase` | Calls/s ≥ `volume_calls_ratio` (3)× usual, ≥ `volume_min_calls` calls | info; **query storm** (warning) at ≥ 3 × the ratio | previous window |
| `queries.load_increase` | Execution time per second ≥ `load_ratio` (3)× usual and ≥ `load_min_added_ms_per_s` (50; 5 in demo) ms/s added | warning | previous window |
| `queries.io_regression` | Blocks read per call ≥ `io_ratio` (3)× usual and ≥ `io_min_reads_per_call` (100), or temp blocks per call ≥ 3× | warning | not evaluated |
| `queries.pattern_change` | Rows per call ≥ `pattern_rows_ratio` (5)× above or below usual | info | not evaluated |
| `queries.possible_n_plus_one` | A parameterised SELECT at ≥ `n_plus_one_min_calls_per_s` (50) calls/s, ≤ `n_plus_one_max_mean_ms` (1 ms), ≤ `n_plus_one_max_rows_per_call` (1) row | info, low confidence | pattern only |

`possible_n_plus_one` says plainly that PostgreSQL alone cannot prove an N+1
pattern: a busy key lookup looks the same.

Every finding carries `metrics` (machine-readable values: current measures,
`baseline_*` medians, `ratio`, `z`, `share_of_load`, `added_load_ms_per_s`,
`window_seconds`) and the comparison basis in its evidence
(`compared with`: `baseline` or `previous window`).

## Slow running queries

`queries.long_running` reads the latest activity snapshot: client backends
in state `active` (parallel workers excluded) whose current statement started
long ago.

- Warning at `slow_query_warn_seconds` (30 s; 10 s in demo), critical at
  `slow_query_critical_seconds` (300 s; 60 s in demo). **Critical opens an
  incident.**
- Also warning when it has run ≥ `slow_query_expected_factor` (20)× the
  statement's usual time per call (from its baseline, matched by
  `query_id`), but never before `slow_query_expected_min_seconds` (5 s).
  When one `query_id` has several identities (users, databases), the largest
  usual time is used, so the rule reports less, not more.
- One finding per execution (key `pid:<pid>:<backend start>:<query start>`),
  so a reused PID or the next statement of the same session is a new finding.
- Evidence: wait event, blocked by, sessions waiting behind it, parallel
  workers, `query_id`.

LockLens never cancels or terminates a query.

## Confidence

Statement findings carry `confidence` (`high`, `medium`, `low`) with reasons.
It grades the **data**, not a cause:

- compared with its own baseline (+2) — or only the previous window;
- the same-hour-of-day profile (+1);
- a long history (twice the warm-up) (+1);
- many calls in the window (+1);
- the window ≥ 95% covered by collections (+1).

High ≥ 5 points, medium ≥ 3, otherwise low. `possible_n_plus_one` is always
low. `long_running` is high (the running time is read directly).

## Impact score

A 0–100 score orders findings by how much they matter **now**. It is the sum
of explained components, times a confidence factor:

| Component | Max | From |
| --- | --- | --- |
| Share of execution time | 30 | `share_of_load` (the statement's share of all statement time in the window) |
| Added load | 20 | `added_load_ms_per_s`, logarithmic; 1,000 ms/s (one busy core) is the maximum |
| Severity | 20 | critical 20, warning 12, info 4 |
| Sessions blocked | 15 | sessions waiting behind a running statement; 10 is the maximum |
| Duration | 15 | how long the finding has been active, or how long the statement has run; logarithmic, an hour is the maximum |

Confidence factor: high 1.0, medium 0.85, low 0.6 (0.8 when none is stated).
Missing measurements add nothing; they never count as zero load. The API
returns every component with its points and a sentence explaining it.

## Correlation and hypotheses

For an active statement anomaly (and for statement incidents, as of their
trigger), LockLens gathers:

- the statement's findings and when each began;
- the target's other findings from an hour before the anomaly until now;
- the statement's and the instance's sampled wait profile (average active
  sessions per wait class) during the anomaly and the equal period before;
- how many other statements became slower at the same time;
- collection gaps; whether plans were saved.

Fixed rules then score these hypotheses (0–1) and rank them:

| Hypothesis | Supported by | Weakened by |
| --- | --- | --- |
| Waiting for locks | Lock share of the statement's samples ≥ 30%; blocking findings (weighted by whether they began *before* the anomaly); the running execution is blocked; deadlocks | Lock share < 5% |
| A different plan or access path | Blocks read per call ≥ 2× usual; temp per call ≥ 2×; slower with unchanged rows per call; I/O waits; the I/O rule | Rows per call also grew |
| More data per call | Rows per call ≥ 2× usual | Rows per call unchanged |
| More executions (traffic, retries, N+1) | Calls/s ≥ 2× usual, with unchanged time per call; the N+1 rule | Calls unchanged |
| Instance-wide resource pressure | Other statements slower at the same time; active sessions ≥ 1.5× before; mostly CPU; connection or temp-file findings | Only this statement is slower; load unchanged |
| Bloat or VACUUM held back | Dead tuples, long transactions, XID age findings | |
| One heavy execution (long-running) | Not blocked; far beyond its usual time, or no baseline (rare statement) | |

Strength: strong ≥ 0.6, moderate ≥ 0.3, weak below. Each hypothesis lists
its supporting and contradicting evidence and the steps that confirm or rule
it out. The investigation also lists a **timeline** and **missing evidence**
(sampling off, no `query_id` samples, no saved plan, gaps, and that
deployments and configuration changes are not recorded). The summary always
says the top hypothesis is "a hypothesis to check, not a confirmed cause".

## Execution plans

Plans are saved **only on request**, per statement:

- **Paste** EXPLAIN output (JSON or text), for example from the SQL editor.
- **Capture generic plan** (PostgreSQL 16+): runs
  `EXPLAIN (FORMAT JSON, GENERIC_PLAN)` on the statement's normalised text
  through the investigation workspace — the same audited, policy-gated,
  read-only transaction as the SQL editor, with the workspace credentials. It
  plans without executing. Only SELECT, INSERT, UPDATE, DELETE, and MERGE in
  the workspace's database are accepted, and only one statement.

Each plan gets a **fingerprint** of its shape (node types, relations,
indexes, join types; not costs). Comparing two plans lists scan changes per
relation (for example `Index Scan using orders_pkey → Seq Scan`), join method
changes, and an estimated cost ratio. **Index candidates** list sequential
scans with a filter and the filtered columns, as hypotheses with the cost of
an index stated; LockLens never creates indexes. Plans are kept as long as
incidents (`LOCKLENS_INCIDENT_RETENTION`).

## Percentiles

`pg_stat_statements` keeps totals per statement, not individual executions,
so p50/p95/p99 latency cannot be derived from it. LockLens shows means over
periods and the spread of those means across the baseline, and **never
estimates percentiles**. APIs return `"percentiles": {"available": false,
"reason": …}`.

## Where to see it

- **Performance** (Workload → Performance): anomalies and running queries
  across the fleet, ranked by impact, with the score breakdown, confidence,
  and evidence.
- **Statement page**: the usual range shaded behind the time-per-call and
  calls charts, the performance analysis (baseline, anomalies, impact,
  likely explanations), and execution plans.
- **Incident page** of a statement incident: possible root causes as of the
  trigger.
- **Settings → Alert rules**: "Slow running queries" and "Baselines and
  anomalies".

APIs: [api.md](api.md#query-performance).

## Limitations

- Baselines need history: a new target or statement compares with the
  previous window for its first `baseline_warmup_points` points.
- Means hide distributions; a regression of a few slow executions among many
  fast ones may not move the mean enough.
- Statements evicted from `pg_stat_statements` (`pg_stat_statements.max`)
  lose activity; evictions are reported in the limitations of findings.
- `queryid` changes across major versions; baselines restart after an upgrade.
- Wait profiles are sampled; very short statements may never be sampled.
- Correlation is only as good as what was collected: application deployments,
  configuration changes, and host events outside node_exporter are not seen.
