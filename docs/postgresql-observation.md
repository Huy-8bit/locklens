# PostgreSQL observation

This document explains what LockLens reads from PostgreSQL, what each value
means, and what it deliberately does not conclude. Links point to the official
PostgreSQL documentation for the current release; the behaviour described here
applies to PostgreSQL 14 and later unless noted.

## Sources

| Source | Used for | Readable by |
| --- | --- | --- |
| [`pg_blocking_pids(pid)`](https://www.postgresql.org/docs/current/functions-info.html#FUNCTIONS-INFO-SESSION) | The blocker → waiter relation. The only source of edges. | every role |
| [`pg_locks`](https://www.postgresql.org/docs/current/view-pg-locks.html) | Finding which backends wait (`granted = false`) and describing what they wait for; `waitstart` for lock wait age | every role |
| [`pg_stat_activity`](https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW) | Session metadata: user, database, application, state, wait event, timestamps, optional query text | every role, but other roles' details need `pg_read_all_stats` |
| [`pg_prepared_xacts`](https://www.postgresql.org/docs/current/view-pg-prepared-xacts.html) | Context for blockers reported as PID 0 | every role |

The SQL lives in [`internal/postgres/sql/`](../internal/postgres/sql/), one
commented file per query.

## How a collection works

All queries run on one connection inside one `READ ONLY` transaction:

1. `server.sql`: version, current database, whether the role has the
   privileges of `pg_read_all_stats`, and `track_activities`.
2. `blocking.sql`: find waiting backends from ungranted `pg_locks` rows, fold
   parallel workers into their leader, and call `pg_blocking_pids()` once per
   waiting backend.
3. `waiting_locks.sql`: the ungranted lock requests, with `waitstart`.
4. `sessions.sql`: `pg_stat_activity` rows for every PID involved in blocking
   plus every client backend with an open transaction.
5. `prepared.sql`: `pg_prepared_xacts`.
6. `SELECT clock_timestamp()`: the server time all ages are measured against.

There is no per-session query, so the cost does not grow as N+1 with the number
of sessions. LockLens' own connections are excluded by backend PID (known from
the connection handshake), not by `application_name`, which any client could
copy.

## Why `pg_blocking_pids()` and not a `pg_locks` self-join

A common approach joins `pg_locks` to itself and treats a granted and an
ungranted lock on the same object as "blocker and waiter". That is wrong in
several ways: it ignores the lock-mode conflict table (two compatible modes do
not block each other), it ignores wait-queue order (a waiter can be blocked by
another *waiter* ahead of it, a "soft block"), and it misses parallel query
groups. PostgreSQL documents `pg_blocking_pids()` as returning sessions that
hold a conflicting lock (hard block) or wait ahead in the queue for a
conflicting lock (soft block). LockLens uses it as the only source of edges and
uses `pg_locks` only to describe the request.

Waiters are found from `pg_locks` rather than from
`pg_stat_activity.wait_event_type = 'Lock'` because `pg_locks` is visible to
every role, while `wait_event_type` is NULL for sessions of other roles when the
observer lacks `pg_read_all_stats`. Calling `pg_blocking_pids()` only for
waiting backends matters: PostgreSQL warns that frequent calls "could have some
impact on database performance" because each needs brief exclusive access to
the lock manager's shared state.

## Fields

### Session (`pg_stat_activity`)

| Field | Meaning | Notes |
| --- | --- | --- |
| `pid` | Backend process ID | |
| `leader_pid` | Parallel group leader if this backend is a parallel worker | Workers are not listed as sessions; they are counted in `parallel_workers` on the leader |
| `database`, `user`, `application_name` | `datname`, `usename`, `application_name` | `application_name` is chosen by the client and is untrusted text |
| `client_addr` | Client IP | NULL for Unix sockets, internal processes, or hidden sessions |
| `backend_type` | e.g. `client backend`, `autovacuum worker` | Only `client backend` sessions are listed as long-running transactions |
| `state` | `active`, `idle`, `idle in transaction`, `idle in transaction (aborted)`, ... | |
| `wait_event_type`, `wait_event` | What the backend waits on right now; `Lock` for heavyweight locks | |
| `xact_start` | Start of the current transaction | NULL outside a transaction |
| `query_start` | Start of the current query, or of the last query when not `active` | **Not** the start of a lock wait |
| `state_change` | When `state` last changed | **Not** the start of a lock wait |
| `backend_xid` | Top-level transaction ID, if assigned | Row-lock waits typically name it |
| `query` | Most recent query text | Collected only with `--include-query`; cut on the server to `--query-max-length` characters; server-side limit `track_activity_query_size` (default 1024 bytes) also applies |
| `details_visible` | Whether the observer may see this session's details | Computed from `pg_has_role(...)` |

### Waiting lock (`pg_locks`, `granted = false`)

`locktype`, `mode`, the target columns (`database`, `relation`, `page`,
`tuple`, `virtualxid`, `transactionid`, `classid`, `objid`, `objsubid`), and
`waitstart`. Relation names are resolved with `regclass` only for the current
database and shared catalogs; relations in other databases are shown by OID.

A row-level conflict usually appears as a `ShareLock` request on the
`transactionid` of the transaction that modified the row, which is why the
report says "typically a row lock held by that transaction". LockLens states
what was requested and never claims which held lock conflicts with it.

## Transaction age, query age, and lock wait age

These are three different durations, all measured against the server clock at
the end of collection:

- **Transaction age** = `server_time − xact_start`. How long the transaction
  has been open. Long-running and idle-in-transaction detection use this.
- **Query age** = `server_time − query_start`. Age of the running statement, or
  time since the last statement started if the session is idle. A statement
  can run for a while before it starts waiting, so this is not the lock wait.
- **Lock wait age** = `server_time − min(waitstart)` over the session's
  ungranted locks. `waitstart` exists since PostgreSQL 14 and "can be null for
  a very short period of time after the wait started"; LockLens then reports
  `unknown`. It never substitutes `query_start` or `state_change`.

Ages are clamped at zero; using the server clock avoids client/server clock
skew.

## Parallel workers

With parallel query, a lock may be held or awaited by a worker process.
`pg_blocking_pids()` "always lists client-visible process IDs" (the leader's),
and "there may be duplicated PIDs in the result". LockLens:

- maps a waiting worker to its leader through `leader_pid` before calling
  `pg_blocking_pids()`;
- removes duplicate edges;
- excludes parallel workers from the session list and counts them on the leader.

When the observer cannot see `leader_pid` (another role's session without
`pg_read_all_stats`), a waiting worker is reported under its own PID.

## Prepared transactions (PID 0)

After `PREPARE TRANSACTION`, a transaction keeps its locks but no longer belongs
to a backend. PostgreSQL documents that a prepared transaction holding a
conflicting lock "will be represented by a zero process ID" in
`pg_blocking_pids()`. LockLens keeps PID 0 as a node of kind
`prepared_transaction`, lists all rows of `pg_prepared_xacts` as context, and
warns that PostgreSQL does not say *which* prepared transaction blocks. All
prepared transactions therefore collapse into one PID 0 node. A prepared
transaction never waits for a lock, so PID 0 is always a blocker.

## Blocking versus deadlocks

Blocking is normal: a waiter proceeds as soon as the blocker commits or rolls
back. A [deadlock](https://www.postgresql.org/docs/current/explicit-locking.html#LOCKING-DEADLOCKS)
is a cycle of waits; PostgreSQL's deadlock detector runs after
[`deadlock_timeout`](https://www.postgresql.org/docs/current/runtime-config-locks.html)
(1 second by default) and aborts one transaction. Consequently:

- a long chain is not a deadlock;
- a cycle that persists in LockLens output is unusual, because the server
  breaks real deadlocks quickly; a cycle can also be an artefact of
  non-atomic collection;
- LockLens labels cycles as `cycle_detected` and never reports a root blocker
  for a cyclic incident, but it never claims a confirmed deadlock.

## Consistency of a collection

- `pg_stat_activity` is read once and cached for the rest of the transaction
  ([cumulative statistics docs](https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-STATS-VIEWS)).
- `pg_locks` and `pg_blocking_pids()` are read live, and `pg_locks` itself "is
  not guaranteed to be entirely consistent" (fast-path locks are gathered per
  backend).
- PostgreSQL also states that `pg_stat_activity` columns of one backend are not
  synchronised with each other.

So a session can end between two reads. A PID that appears in an edge but has
no `pg_stat_activity` row is kept as a `missing` placeholder with a warning
instead of being dropped or crashing the report. The snapshot records
`"atomic": false`.

## Required privileges

LockLens never needs a superuser.

| Capability | Minimum role (LOGIN + CONNECT) | With `pg_read_all_stats` / `pg_monitor` |
| --- | --- | --- |
| Blocking relations (`pg_blocking_pids`, `pg_locks`) | yes | yes |
| Lock wait age (`pg_locks.waitstart`) | yes | yes |
| Prepared transactions | yes | yes |
| Own sessions' details | yes | yes |
| Other roles' state, timestamps, wait events, client address, backend type | hidden (NULL) | yes |
| Other roles' query text | hidden (`<insufficient privilege>`, discarded) | yes, with `--include-query` |
| Other roles' long-running transactions (not involved in blocking) | invisible | yes |

PostgreSQL documents that ordinary users "can only see all the information about
their own sessions", that in other rows "many columns will be null", and that
superusers and roles with the privileges of `pg_read_all_stats` "can see all
the information about all sessions" ([section 27.2.2](https://www.postgresql.org/docs/current/monitoring-stats.html#MONITORING-STATS-VIEWS)).
With a minimum role, identifying columns (PID, database, user, application
name, transaction IDs) remain visible; this was verified by the integration
tests on PostgreSQL 14–18.

With a restricted role LockLens still produces a report, marks hidden sessions
with `details_visible: false`, renders them as `[details hidden by permissions]`,
and adds a `restricted_visibility` warning, so a partial report is never
presented as complete.

Recommended setup:

```sql
CREATE ROLE locklens_observer LOGIN PASSWORD '...';
GRANT pg_read_all_stats TO locklens_observer;   -- or: GRANT pg_monitor TO locklens_observer;
```

`pg_monitor` also grants `pg_read_all_settings` and `pg_stat_scan_tables`,
which LockLens does not use; `pg_read_all_stats` is the narrower choice.

## Session settings

Every LockLens connection sets, through startup parameters:

| Setting | Value | Why |
| --- | --- | --- |
| `application_name` | `locklens` | Recognisable in `pg_stat_activity` |
| `default_transaction_read_only` | `on` | Observation is enforced by the server, not just promised |
| `statement_timeout` | `--query-timeout` | Server-side backstop for slow collections |
| `lock_timeout` | 2 s | Never queue behind a strong lock on a catalog |
| `idle_in_transaction_session_timeout` | 60 s | A crashed LockLens cannot leave an open transaction |

## Supported versions

PostgreSQL 14 or later. Every column LockLens reads exists from 14 on (the
newest is `pg_locks.waitstart`, added in 14), so there is no per-version SQL.
Older servers are rejected with an explicit error rather than producing a
silently degraded report. Versions actually tested are listed in
[testing.md](testing.md#postgresql-versions).

## Further reading

- [Explicit locking and lock modes](https://www.postgresql.org/docs/current/explicit-locking.html)
- [Wait events](https://www.postgresql.org/docs/current/monitoring-stats.html#WAIT-EVENT-TABLE)
- [Lock management settings](https://www.postgresql.org/docs/current/runtime-config-locks.html)
- [Predefined roles](https://www.postgresql.org/docs/current/predefined-roles.html)
