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
- A session that ends mid-collection is still shown with the metadata cached
  at the start of the collection, and its lock request may already be missing
  from `waiting_locks`. PIDs absent from the cached `pg_stat_activity` view
  appear as `missing` placeholders. See
  [postgresql-observation.md](postgresql-observation.md#consistency-of-a-collection).
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

## Server and monitoring

- **Sampling.** Everything is sampled at the collector intervals. Blocking
  shorter than the activity interval, short connection spikes, and brief
  replication lag between samples are not seen.
- **Workload analysis** depends on `pg_stat_statements`: only window means
  (no percentiles or per-execution samples), only statements still in the
  server's hash table (`pg_stat_statements.max`; evictions are reported but
  their counts are lost), and `queryid` is not stable across major versions.
  The first collection after a start is a baseline, so short windows right
  after a start have little coverage.
- **Query baselines and anomalies** need history: until a statement has
  `baseline_warmup_points` covered points, rules compare with the previous
  window only. Correlated root causes are ranked hypotheses from collected
  evidence, never confirmed causes. See [query-intelligence.md](query-intelligence.md#limitations).
- **Change timeline.** Settings and server facts are compared every minute,
  the schema every 15 minutes: a change is known to have happened between two
  readings, not at an exact moment (restarts and reloads are exact, from
  `pg_postmaster_start_time()` and `pg_conf_load_time()`). Schema comparison
  is skipped while the catalog is cut at its limits. Deploys and migrations
  are known only when a person or pipeline reports them.
- **Table statistics** cover the databases of the target's scope (by default
  only the connection string's, at most 20) and only the largest tables of
  each; tuple counts are estimates, and a dead-tuple ratio is not measured
  bloat. Each extra database uses one more connection. What-if checks
  (HypoPG) run only on the connection string's database.
- **Server log events** need the log agent on the database host (managed
  services usually do not give file access) and the logging settings that
  produce them. Durations from the slow statement log describe only
  executions slower than `log_min_duration_statement`: their percentiles are
  of those executions, not of all executions. Without cgo, the agent
  normalises statements lexically (constants replaced, no parser
  fingerprint). Redaction removes quoted values and constants; it cannot
  recognise values the server writes without quotes.
- **Findings are heuristics** with explicit evidence and limitations, not
  confirmed root causes. Thresholds are generic defaults; tune them per
  environment. Demo-mode thresholds are for the lab only.
- **Incidents** keep at most 1,000 events or 5 MiB and stop after 2 hours;
  the in-memory pre-trigger buffer is lost on restart, so an incident opened
  shortly after a restart has little pre-trigger context.
- **Restarts and store outages** leave holes in history. Batches dropped from
  the bounded write queue are recorded as gaps, not recovered.
- **Host metrics** require node_exporter on the database host; managed
  services cannot provide them. In Docker Desktop and similar, node_exporter
  sees the Linux VM.
- **Three fixed roles** (administrator, operator, viewer), no SSO, no
  per-environment permissions, no general audit log; no in-place master key
  rotation; no built-in TLS termination.
- **Scale.** Tested locally with up to 20 targets on one machine
  ([test-report.md](test-report.md)). Larger fleets have not been measured;
  storage grows with the number of targets and their intervals
  ([storage-and-retention.md](storage-and-retention.md#storage-growth)).
- **Several servers** share a store by leases: when a server crashes, its
  targets are collected again only after their leases expire (about a
  minute), and that time is a gap. For a moment around a takeover two servers
  can collect the same target. Login rate limiting is per server, and a
  running workspace statement can be cancelled only on the server that runs
  it (use sticky sessions in the load balancer). Messages between servers
  (browser events) larger than about 8 KB are not forwarded.

## Unsupported behaviour

- PostgreSQL older than 14 is rejected (targets and the monitoring store).
- No kill, cancel, or terminate actions; no configuration changes, index or
  vacuum operations, failover, or any other automatic remediation.
- The CLI has no configuration file and no history beyond snapshot files you
  save; history lives only in the server's monitoring store.
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
- The server's `/metrics`, `/healthz`, and `/readyz` are unauthenticated by
  design; they expose no credentials or query text.
- See [security.md](security.md) and [SECURITY.md](../SECURITY.md).
