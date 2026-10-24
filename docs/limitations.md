# Limitations

What LockLens cannot see, cannot guarantee, or does not do. Each item links to
the detailed explanation.

## Visibility

- **Without `pg_read_all_stats`**, other roles' sessions show only PID,
  database, user, application name, and transaction IDs. Their state, timings,
  wait events, and query text are unknown, and their long-running transactions
  are invisible unless they take part in blocking. The report carries a
  `restricted_visibility` warning. See
  [postgresql-observation.md](postgresql-observation.md#required-privileges).
- **`track_activities = off`** stops the server from maintaining state, wait
  events, and query text. LockLens warns but cannot recover the data.
- **Query text** is limited by the server's `track_activity_query_size`
  (1024 bytes by default) before LockLens applies `--query-max-length`.
- **Prepared transactions** are reported by PostgreSQL as PID 0 without saying
  which one blocks. All of them collapse into a single node, and LockLens lists
  every prepared transaction as context.
- **Relation names** are resolved only for the database LockLens is connected
  to (and shared catalogs). Locks on relations in other databases show OIDs.
- **Parallel workers** of another role's session cannot be mapped to their
  leader without `pg_read_all_stats` and appear under their own PID.
- **Lock wait age** depends on `pg_locks.waitstart`, which can be NULL for a
  moment after a wait begins; it is then `unknown`.
- **Blocker detail**: LockLens shows what each waiter requested, not which lock
  the blocker holds.
- **Lightweight locks and other waits** (LWLocks, I/O, client reads) are not
  lock blocking in the `pg_blocking_pids()` sense and are not part of the graph.
  They are visible only as `wait_event_type`/`wait_event` of listed sessions.
- **One cluster at a time.** `pg_stat_activity` and `pg_locks` are
  cluster-wide, so one connection observes all databases of that server, but
  replicas and other clusters need their own LockLens run.

## Consistency

- A report is assembled from several catalog reads. `pg_stat_activity` is
  cached for the collection's transaction, while `pg_locks` and
  `pg_blocking_pids()` are read live, and `pg_locks` itself is not fully
  consistent for fast-path locks. **A snapshot is not atomic.** See
  [postgresql-observation.md](postgresql-observation.md#consistency-of-a-collection).
- Sessions that end mid-collection appear as `missing` placeholders.
- A cycle in the observed data is labelled as such but is **not** a confirmed
  deadlock; PostgreSQL resolves real deadlocks after `deadlock_timeout`. See
  [blocking-graph.md](blocking-graph.md#cycle).
- `watch` samples. Contention shorter than the interval can be missed
  entirely; LockLens is not an event log.
- `collected_at` uses the client clock; ages use the server clock.

## Overhead

- Each collection runs five catalog queries plus a clock read in one
  read-only transaction.
- `pg_blocking_pids()` needs exclusive access to the lock manager's shared
  state for a short time and is called once per *waiting* backend. Reading `pg_locks` also locks the lock
  manager briefly. PostgreSQL warns that very frequent access to either can
  affect performance. `watch` therefore defaults to 2 s and refuses intervals
  under 500 ms. No benchmarks have been published for LockLens.
- On a server with hundreds of waiting backends, a collection calls
  `pg_blocking_pids()` hundreds of times; prefer longer intervals there.
- The graph's transitive counts are O(V·(V+E)); see
  [blocking-graph.md](blocking-graph.md#complexity).

## Unsupported behaviour

- PostgreSQL older than 14 is rejected.
- No kill, cancel, or terminate actions; no automatic remediation.
- No configuration file, no persistent history beyond snapshot files you save.
- Snapshot files are readable only by LockLens builds that support their
  `schema_version` (currently 1).
- Only the database versions listed in [testing.md](testing.md) have been
  tested. Managed services or forks that change `pg_stat_activity` or
  `pg_locks` semantics have not been tested.

## Security considerations

- A `--dsn` on the command line is visible in the process list; use
  `LOCKLENS_DSN`, `PGPASSWORD`, or `~/.pgpass`.
- Redaction replaces exact secret values of three or more characters. Shorter
  passwords are not searched for in error text (that would mangle unrelated
  text); PostgreSQL and pgx do not echo passwords in their errors.
- Snapshot files are `0600` but still contain operational metadata (user,
  application, database names, client addresses) and, with `--include-query`,
  SQL text. Handle them as sensitive.
- See [SECURITY.md](../SECURITY.md).
