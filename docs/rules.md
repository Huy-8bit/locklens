# Rules and findings

LockLens evaluates a fixed set of deterministic rules after every activity
collection of a target. Rules are plain Go functions in
[backend/internal/rules](../backend/internal/rules); there is no machine learning and no
external service.

## What a finding contains

| Field | Meaning |
| --- | --- |
| `rule_id`, `rule_version` | Stable ID; the version changes whenever the rule's logic changes |
| `key` | What the finding is about (a root blocker, a query identity, a table, a slot, …) |
| `severity` | `info`, `warning`, `critical` |
| `status` | `active`, `insufficient_data`, `resolved` |
| `observed` | Facts, phrased as observations ("pg_blocking_pids() reported this blocking in 4 consecutive samples over 35s") |
| `evidence` | Named values with units and their source (`pg_stat_activity.xact_start`, `pg_stat_statements deltas`, …) |
| `explanation` | What the observation means in general |
| `possible_explanations` | Candidate causes — **never** presented as a confirmed root cause |
| `next_checks` | What a person should look at next |
| `limitations` | Why the inference may be wrong or incomplete (sampling, non-atomic snapshots, restricted visibility, evictions, resets, …) |
| `first_seen`, `last_seen`, `resolved_at` | Lifecycle, kept in the store |

**Missing data never makes a rule pass.** When the inputs are not available
(no collection yet, extension missing, too little coverage, unreadable
setting), the rule produces an `insufficient_data` finding that says what is
missing, instead of silently reporting "OK".

Findings with the same rule and key are one finding over time: it is created
when first seen, updated while it stays active, and resolved when the
condition is gone.

## Rules

Thresholds are configurable in **Settings → Rule thresholds**. Defaults are
shown; values in parentheses apply in demo mode (`LOCKLENS_DEMO_MODE`), which
shortens windows so lab scenarios trigger within minutes.

Rules marked **trigger** can open incidents ([incidents.md](incidents.md)).

### `blocking.long_chain` — persistent blocking (trigger)

For each root blocker in the blocking graph (edges from
`pg_blocking_pids()`): fires when it blocks at least `blocking_min_blocked` =
1 session in total, in at least `blocking_min_samples` = 3 consecutive
samples, for at least `blocking_min_seconds` = 30 s (20 s). Critical when it
blocks ≥ `blocking_critical_blocked` = 10 sessions or lasts ≥
`blocking_critical_seconds` = 300 s. Prepared transactions (PID 0) and cycles
get their own titles; a cycle in sampled data is explicitly **not** called a
confirmed deadlock.

### `blocking.idle_in_tx_blocker` — idle-in-transaction root blocker (trigger)

Critical when a root blocker is `idle in transaction` (or aborted) in at
least `idle_blocker_min_samples` = 2 consecutive samples. Such a session runs
no statement, so it cannot finish by itself; only its client can commit or
roll back. LockLens does not terminate it.

### `connections.pressure` — connection slots running out (trigger)

Usage = used connections / (`max_connections` − reserved slots). Warning at ≥
`connections_warn_ratio` = 0.8, critical at ≥ `connections_critical_ratio` =
0.95, after `connections_min_samples` = 2 consecutive samples.

### `transactions.long` — long-running transactions

Client backends whose transaction has been open ≥
`long_transaction_warn_seconds` = 300 s (60 s); critical at ≥
`long_transaction_critical_seconds` = 1800 s (600 s). At most five, oldest
first. Long transactions hold back vacuum and may hold locks.

### `queries.mean_regression` — slower per call (trigger)

Compares two consecutive windows of `workload_window_seconds` = 900 s (120 s)
from `pg_stat_statements` deltas. Fires for identities with ≥
`regression_min_calls` = 50 (30) calls in both windows whose mean execution
time grew by a factor ≥ `regression_mean_ratio` = 2 and by ≥
`regression_min_delta_ms` = 5 ms. Needs ≥ `workload_min_coverage` = 80%
coverage of both windows. Top five. See [query-analysis.md](query-analysis.md).

### `queries.call_volume_increase` — more calls

Same windows; info when calls grew by a factor ≥ `volume_calls_ratio` = 3
with ≥ `volume_min_calls` = 500 (200) calls in the current window.

### `temp_files.increase` — temporary-file writes increased

Temporary bytes written in the last `temp_window_seconds` = 900 s (120 s)
versus the window before: warning when the current window wrote ≥
`temp_min_bytes` = 64 MiB (16 MiB) and at least `temp_ratio` = 3 times the
previous window (or the previous window wrote nothing). Needs 70% counter
coverage of both windows.

### `deadlocks.increase` — deadlocks detected by PostgreSQL

Warning when `pg_stat_database.deadlocks` increased in the last
`deadlock_window_seconds` = 900 s (300 s). These are deadlocks PostgreSQL
already detected and resolved by cancelling a transaction; they are counted,
not reconstructed.

### `vacuum.dead_tuples` — dead tuples accumulating

Per table (the 200 largest in the connected database): warning when the dead
tuple estimate is ≥ `dead_tuples_min` = 10,000, the dead share is ≥
`dead_tuples_ratio` = 0.2, and the table was not vacuumed (manually or by
autovacuum) in the last `dead_tuples_vacuum_age_seconds` = 86,400 s. Up to
ten tables. Tuple counts are statistics estimates; a dead-tuple ratio is not a
measurement of bloat. Open long transactions are mentioned because they
prevent cleanup.

### `vacuum.xid_age` — transaction ID wraparound risk

Per database: `age(datfrozenxid)` as a fraction of
`autovacuum_freeze_max_age`. Warning at ≥ `xid_age_warn_fraction` = 0.5,
critical at ≥ `xid_age_critical_fraction` = 0.9.

### `replication.replica_lag` — replica behind

On a primary: a replica whose replay lag is ≥ `replica_lag_bytes` = 64 MiB
(16 MiB) for `replica_lag_min_samples` = 2 consecutive samples. On a standby:
received but not yet replayed WAL ≥ the same threshold. Time-based lag fields
are shown as evidence when PostgreSQL provides them (they are empty when the
primary is idle).

### `replication.slot_retained_wal` — slot retains WAL (trigger)

A replication slot whose `restart_lsn` keeps ≥ `slot_retained_bytes` = 1 GiB
(32 MiB) of WAL. Critical when `wal_status` is `unreserved` or `lost`.
Inactive slots are called out because nothing is consuming them.

### `wal.archiver_failures` — WAL archiving failing

Warning when `pg_stat_archiver.failed_count` increased in the last
`archiver_window_seconds` = 900 s (300 s), with the last failed WAL file and
time as evidence.

## Evaluation and history

- Rules run in the incident recorder after each activity collection, using the
  in-memory history of the target (up to 360 samples) and the latest results of
  the other collectors.
- Findings are synchronised to the store: new ones inserted, active ones
  updated, missing ones resolved. History is kept for
  `LOCKLENS_FINDING_RETENTION`.
- Changing thresholds takes effect at the next evaluation. Rule versions are
  stored with findings and incidents, so old evidence stays interpretable.

## Adding a rule

1. Add an ID constant and version in
   [backend/internal/rules/types.go](../backend/internal/rules/types.go), and thresholds with
   defaults and validation if needed.
2. Implement it as a function `func(Input, Thresholds) []Finding` that returns
   `insufficient(...)` when its inputs are missing.
3. Register it in [backend/internal/rules/engine.go](../backend/internal/rules/engine.go).
4. Add table-driven tests in
   [backend/internal/rules/rules_test.go](../backend/internal/rules/rules_test.go), including
   the missing-data case.
5. Document it here.
