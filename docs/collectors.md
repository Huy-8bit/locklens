# Collectors

`locklens server` runs one worker per enabled target. A worker owns a small
connection pool (1–5 connections, default 2) and runs that target's collectors
**one at a time**, so a slow collector delays the next one instead of piling up
connections. A global semaphore (`LOCKLENS_MAX_CONCURRENT_COLLECTIONS`, default
4) caps collector runs across all targets, so adding targets never increases
concurrency against the store or the network without bound.

Every collection runs in a `READ ONLY` transaction on sessions with
`statement_timeout` (30 s), `lock_timeout` (2 s), and
`idle_in_transaction_session_timeout` (60 s) set, under the
application name `locklens`. LockLens' own backend PIDs are excluded from
activity data. Collectors never run DDL or DML on targets, never call
`pg_cancel_backend`/`pg_terminate_backend`, and never change settings.

## Schedule

`interval` is the target's activity interval (5–3600 s, default 10 s).

| Collector | Interval | Timeout | Sources | Produces |
| --- | --- | --- | --- | --- |
| `activity` | `interval` | min(interval, 15 s) | `pg_stat_activity`, `pg_blocking_pids()`, `pg_locks` (waiting locks only), `pg_prepared_xacts`, `pg_stat_progress_vacuum`, `max_connections`, reserved connections | Session counts by state, connection usage, blocking graph, long transactions, statements running for long (with `query_id`, PostgreSQL 14+, to compare with their baseline), vacuum progress; the full activity snapshot is stored for every collection with blocking or long transactions, and every 5 minutes otherwise |
| `load` | 2 s (`LOCKLENS_LOAD_SAMPLE_INTERVAL`) | 5 s | `pg_stat_activity` (labels, state, wait event, `query_id`; never the query text) | Active session sampling: sessions that are not idle, aggregated into 15-second buckets; see [Database load](#database-load-active-session-sampling) |
| `database` | max(interval, 15 s) | 15 s | `pg_stat_database`, `pg_stat_bgwriter`, `pg_stat_checkpointer` (17+), `pg_stat_io` (16+), WAL LSN, `pg_stat_archiver`, `pg_postmaster_start_time()` | Commit/rollback rates, cache hit ratio, temp files/bytes, deadlocks, checkpoints, background writer, I/O, WAL rate, archiver deltas |
| `statements` | max(interval, `LOCKLENS_STATEMENTS_INTERVAL`), 15 s by default | 30 s | `pg_stat_statements(false)` (counters without texts), `pg_stat_statements_info`; texts only for statements not seen before | Per-query deltas per collection; see [query-analysis.md](query-analysis.md) |
| `replication` | max(interval, 30 s) | 15 s | `pg_stat_replication`, `pg_replication_slots`, `pg_stat_wal_receiver`, recovery functions | Role, replicas and lags, slots and retained WAL, standby receive/replay state |
| `tables` | 5 min | 60 s | `pg_stat_user_tables`, `pg_stat_user_indexes`, `pg_class`, `pg_database` | Tuple estimates, vacuum/analyze times and counts, sizes (200 largest tables), XID age per database, database sizes |
| `table_activity` | max(interval, 60 s) | 15 s | `pg_stat_user_tables` counters only (no size functions, no locks) | Per-table rates of the 20 busiest tables: sequential scans and rows read, index scans, rows written, dead rows (`table.*` metrics, Hot tables) |
| `changes` | 1 min | 15 s | `pg_settings` (name, value, unit, source, pending restart), `pg_postmaster_start_time()`, `pg_conf_load_time()`, `pg_is_in_recovery()`, version | The [change timeline](#change-detection): settings changes, restarts, promotions, upgrades |
| `catalog` | 15 min, and on request (Optimize → Analyse now) | 60 s | `pg_index`, `pg_class`, `pg_constraint`, `pg_stat_user_tables`, `pg_stat_user_indexes`, `pg_stats` (`null_frac`, `n_distinct`, `correlation` only), `pg_extension`, planner settings | The schema for the [query optimizer](query-optimizer.md) |
| `host` | max(interval, 15 s) | 10 s (5 s HTTP) | node_exporter `/metrics` (optional) | CPU, memory, disk and network throughput, filesystem usage |

Database and table sizes run only in the 5-minute `tables` collector. Size
functions take an `ACCESS SHARE` lock per relation; they run under a 250 ms
`lock_timeout` and fall back to `relpages`-based estimates when a relation is
locked exclusively, so a long-running DDL statement on a target never blocks
LockLens (and LockLens never queues behind it for long).

The first run of each collector is staggered by 300 ms per collector.

### Several databases

`tables`, `table_activity`, and `catalog` read per database. With a target's
`database_scope` of `all` or `list`, they run on the connection string's
database and then on each other database of the scope (listed from
`pg_database` every 5 minutes, at most 20), over one extra connection per
database with the same credentials. A database that cannot be reached is
skipped for a minute and logged; the other databases are still collected.
Cluster-wide data (database sizes and ages, sessions, statements) comes from
the main connection only. Per-table metrics are labelled
`database.schema.table`.

## Change detection

The `changes` collector compares each reading of `pg_settings` and the
server facts with the previous one; the `catalog` collector does the same for
each database's tables and index definitions. A difference is stored as a
change event:

| Kind | Detected from | Time |
| --- | --- | --- |
| `restart` | `pg_postmaster_start_time()` changed | Exact (the new start time) |
| `settings` | Values in `pg_settings` changed (also settings that appeared or disappeared, and `pending_restart`) | The reload (`pg_conf_load_time()`) when it falls in the window, else the reading |
| `promotion`, `demotion` | `pg_is_in_recovery()` changed | Between two readings |
| `upgrade` | `server_version_num` changed | Between two readings |
| `schema` | Tables created or dropped; indexes created, dropped, redefined, or becoming invalid/valid | Between two catalog readings (15 min); skipped while the catalog is cut at its limits |

The first reading after a target is added is the baseline. Settings that can
hold secrets (names containing `conninfo`, `command`, `password`,
`passphrase`, `secret`, `key_file`) are compared by a SHA-256 fingerprint,
never stored. Reported changes (deploys, migrations) come from the API.

## Server log agent

The server log holds what the statistics views cannot: individual slow
executions and their plans, lock waits with their holders, deadlocks,
autovacuum durations, temporary files, checkpoint durations, and errors.
`locklens agent` runs next to the log files and reads them; it never connects
to PostgreSQL.

| Event | Log message | Needs |
| --- | --- | --- |
| `slow_query` | `duration: … ms  statement: …` | `log_min_duration_statement` ≥ 0 |
| `plan` | `duration: … ms  plan: …` | `auto_explain` loaded, `auto_explain.log_min_duration` ≥ 0 |
| `lock_wait` | `process N still waiting for … after … ms` / `acquired …` | `log_lock_waits = on` |
| `deadlock` | `deadlock detected` with the cycle | always |
| `autovacuum`, `autoanalyze` | `automatic vacuum/analyze of table …` | `log_autovacuum_min_duration` ≥ 0 |
| `temp_file` | `temporary file: path …, size …` | `log_temp_files` ≥ 0 |
| `checkpoint` | `checkpoint complete: …` | `log_checkpoints = on` |
| `error` | `ERROR`, `FATAL`, `PANIC` messages with their SQLSTATE | always |

Before an event leaves the host, statement texts are normalised (constants
become `$1`, `$2`, …, with PostgreSQL's parser in cgo builds, a lexical pass
otherwise), constants in plan conditions become `?`, quoted values in error
messages become `"?"` (quoted object names stay), and error DETAIL lines are
dropped. Other messages (connections, notices) are not kept. The agent reads
files from their end on its first start (`--from-start` to read history),
follows rotation and truncation, keeps its read positions, and spools
batches to disk while the server cannot be reached. Events are deduplicated
by an ID derived from the message, so a batch sent twice is stored once.

### Statement texts

`pg_stat_statements` keeps statement texts in a file that the extension reads
in full whenever texts are requested, the most expensive part of reading it.
The `statements` collector therefore reads counters with
`pg_stat_statements(false)` and asks for texts only for statements it has not
seen before (or for all of them once after query text collection is turned
on). The text is needed once per statement: its command class and text do not
change while the entry exists. This is what makes a 15-second interval cheap.

The `table_activity` rates are shown as **Hot tables** on the instance's
Activity tab:

![Hot tables](images/hot-tables.png)

### Adaptive sampling

While a target has an open incident, a statement running for long, blocked
sessions, or a statement slower than its baseline, it is collected faster for
`LOCKLENS_BURST_DURATION` (10 minutes, extended while the problem lasts):
`activity` every 3 s, `database`, `statements`, and `host` every 5 s,
`replication` every 10 s, `table_activity` every 15 s. Evidence around a
problem is denser; quiet targets cost no more than before. The collector
status shows `burst` and the interval in effect. `LOCKLENS_ADAPTIVE_SAMPLING=false`
turns it off.

### Collector time budget

Each run is timed. A collector whose runs take more than
`LOCKLENS_COLLECTOR_BUDGET_PERCENT` (10%) of its interval three times in a row
runs half as often, up to 8 times less often, and returns to its normal pace
step by step after 20 cheap runs. The collector status says so
(`throttle_reason`), and the instance's Collection card shows "slowed". A
target that is expensive to observe is thus protected from LockLens itself.

## Database load (active session sampling)

The `load` collector samples `pg_stat_activity` every few seconds (default 2 s,
PostgreSQL 14+) and answers "what was the database busy with at 14:32?":

- **Which sessions.** Client backends, parallel workers, and autovacuum workers
  that are not idle. LockLens' own connections are excluded.
- **Wait class.** `wait_event_type`, or `CPU` for an active session without a
  wait event (it is running, or waiting on something PostgreSQL does not
  instrument: this is not a measurement of CPU usage), `IdleTx` for a session
  idle in a transaction, and `Unknown` when the observer cannot see the
  session's state.
- **Statement.** `pg_stat_activity.query_id`, which is the top-level
  statement's `pg_stat_statements` queryid. PostgreSQL computes it only when
  `compute_query_id` is `on`, or `auto` with pg_stat_statements loaded;
  otherwise statements are grouped as "without a query ID". The text is never
  read here: statements are linked to the identities the `statements`
  collector stores, with text only where the instance collects it.
- **Aggregation.** Samples are counted per 15-second bucket and group (wait
  class and event, backend type, database, user, application, query ID); at
  most 300 groups per bucket, the rest merged into an "(other)" application.
  Each bucket records how many samples (ticks) it holds.
- **AAS.** Average active sessions of a group = sessions seen ÷ samples taken.
  A bucket without samples is a gap, never zero load. Sessions idle in a
  transaction are reported separately and are not counted as load.

Sampling is cheap (one `pg_stat_activity` read) but runs often: it goes
through the same per-target sequence and global concurrency limit as other
collectors, so a slow collector delays a few samples instead of overlapping
with them. Turn it off with `LOCKLENS_LOAD_SAMPLING=false`. Its status is
saved, and announced to the UI, only when it starts or stops failing.

## Capabilities

On connect (and every 10 minutes), the worker detects:

| Capability | How | Effect when missing |
| --- | --- | --- |
| Server version | `server_version_num` | Below 14: the target is rejected as unsupported |
| Standby | `pg_is_in_recovery()` | Standby-specific replication view and metrics |
| Full visibility | membership in `pg_read_all_stats` (or superuser) | Other roles' sessions show no query, state details, or statement rows; target is `degraded` with a `restricted_visibility` warning |
| `track_activities` | setting | Activity data incomplete; target is `degraded` |
| `pg_stat_io` | version ≥ 16 | I/O metrics absent |
| `pg_stat_checkpointer` | version ≥ 17 | Checkpoint counters read from `pg_stat_bgwriter` instead |
| `pg_stat_statements` | extension installed in the connected database **and** readable (probed under a savepoint) | Statements collector disabled with the exact reason, e.g. "extension pg_stat_statements is not installed in the database LockLens connects to" or the PostgreSQL error when it is not preloaded |

A disabled collector is shown with its reason on the target settings and
overview pages; it is not an error.

## Counters, deltas, and resets

PostgreSQL statistics are cumulative counters. LockLens stores derived values
computed at ingest time against the previous sample of the same target:
per-second rates (`*.per_s`) or per-collection changes (`*.delta`) for
discrete events such as deadlocks.

A change is **unknown** (stored as `NULL`, shown as a gap, never as `0`) when:

| Reason | Detection |
| --- | --- |
| `first_sample` | No previous sample since the worker started |
| `server_restart` | `pg_postmaster_start_time()` changed |
| `stats_reset` | The scope's `stats_reset` timestamp changed |
| `counter_decreased` | A counter went down |
| `collection_gap` | More than 3 collector intervals passed since the previous sample |
| `no_common_scopes` | No database/scope present in both samples |

Deltas are computed per scope (each database, bgwriter, checkpointer, I/O,
WAL, archiver), so dropping or creating a database does not corrupt the others.
When a delta is unknown, a collection gap with the reason is recorded.

The cache hit ratio is `blks_hit / (blks_hit + blks_read)` over the interval.
It covers shared buffers only; reads served by the operating system cache count
as misses.

## Collection gaps

History never hides missing data. A gap record (`collector`, `from`, `to`,
`reason`) is written when:

- a collector did not succeed for more than 2 intervals plus its timeout
  (connection lost, timeouts, errors) — written when it next succeeds;
- counters could not be compared (reasons above);
- the store was unavailable long enough that the bounded write queue dropped
  data ("data dropped: monitoring store unavailable or write queue full").

Charts break their lines at gaps, and the metrics API returns gaps with the
series.

## Storage writes

Collectors never write to the store directly. They enqueue batches to a single
writer with a bounded queue (`LOCKLENS_WRITE_QUEUE`, default 2048 batches).
When the store is down, writes are retried; when the queue is full, the
**oldest** batch is dropped, counted in `locklens_store_dropped_batches_total`,
and recorded as a gap once the store is back. Memory use is therefore bounded
and the loss is visible.

The in-memory view used for live pages keeps up to 360 points per metric and
120 statement collections per target.

## Target states

| State | Criteria |
| --- | --- |
| `pending` | Never attempted a connection, or the activity collector has not run yet |
| `healthy` | Connected, the last activity collection succeeded within 3 × interval + 15 s, no enabled collector reports an error, full visibility |
| `degraded` | Connected and collecting, but an enabled collector's last run failed, or the role lacks `pg_read_all_stats`, or `track_activities` is off |
| `unreachable` | The connection failed, or no successful activity collection within 3 × interval + 15 s |
| `disabled` | Collection switched off in the target settings |

`freshness_seconds` is the age of the last successful activity collection.
Reconnects back off exponentially from the interval up to 1 minute. A failing
target never affects other targets: each has its own worker, pool, and
timeouts.

## Host metrics (optional)

PostgreSQL does not expose CPU, memory, or disk. If a target has a
node_exporter URL, the `host` collector scrapes it (5 s timeout, responses larger
than 8 MiB rejected) and derives CPU utilisation, available/total memory, disk and network
throughput, and filesystem usage per mount point.

Requirements and caveats:

- node_exporter must run **on the database host** (or in the same VM/pod
  network namespace as PostgreSQL); otherwise the numbers describe another
  machine.
- On Docker Desktop, OrbStack, and similar, a containerised node_exporter sees
  the Linux VM, not the macOS or Windows host.
- Managed services (RDS, Cloud SQL, Azure) do not offer node_exporter; use the
  provider's metrics instead. The UI then shows host metrics as
  "Unavailable" — never as zero.

## Metrics

Metric names available to `GET /api/v1/targets/{id}/metrics` and the charts.
Per-label series (per database, replica, slot, mount point) are stored with a
label.

| Group | Metrics |
| --- | --- |
| Sessions | `sessions.active`, `sessions.idle`, `sessions.idle_in_transaction`, `sessions.waiting_lock`, `sessions.unknown_state`, `sessions.client_total` |
| Connections | `connections.used`, `connections.max`, `connections.reserved`, `connections.usage_ratio` (used / (max − reserved)) |
| Blocking | `blocking.blocked_sessions`, `blocking.root_blockers`, `blocking.max_lock_wait_seconds` |
| Transactions | `transactions.open`, `transactions.long`, `transactions.oldest_age_seconds`, `transactions.idle_in_transaction_oldest_age_seconds` |
| Throughput | `xact.commit.per_s`, `xact.rollback.per_s`, `blocks.read.per_s`, `blocks.hit.per_s`, `cache.hit_ratio` |
| Temp files, deadlocks | `temp.files.per_s`, `temp.bytes.per_s`, `deadlocks.delta` |
| Checkpoints, bgwriter | `checkpoints.timed.delta`, `checkpoints.requested.delta`, `checkpoint.buffers_written.per_s`, `bgwriter.buffers_clean.per_s`, `bgwriter.maxwritten_clean.delta`, `buffers.alloc.per_s`, `buffers.backend.per_s` (≤ 16) |
| I/O (16+) | `io.reads.per_s`, `io.writes.per_s`, `io.extends.per_s` |
| WAL, archiver | `wal.bytes.per_s` (primary), `wal.received_bytes.per_s` (standby), `archiver.archived.delta`, `archiver.failed.delta` |
| Statements | `statements.calls.per_s`, `statements.exec_ms.per_s` |
| Replication | `replication.replicas`, `replication.replay_lag_bytes{replica}`, `replication.max_replay_lag_bytes`, `slots.retained_bytes{slot}`, `slots.max_retained_bytes`, `standby.replay_delay_bytes` |
| Databases | `db.size_bytes{database}`, `db.total_size_bytes`, `db.xid_age{database}` |
| Host | `host.cpu_utilization_ratio`, `host.memory_available_bytes`, `host.memory_total_bytes`, `host.disk_read_bytes.per_s`, `host.disk_write_bytes.per_s`, `host.network_receive_bytes.per_s`, `host.network_transmit_bytes.per_s`, `host.filesystem_used_ratio{mountpoint}` |

When a chart's range has more samples than 400 buckets, values are aggregated
per bucket: `sum` for `*.delta`, `max` for `max_*`/`oldest*` gauges, `avg`
otherwise. Each bucket reports how many samples it had and how many were known.
