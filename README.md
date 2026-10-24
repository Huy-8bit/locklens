# LockLens

A Go CLI for investigating PostgreSQL blocking chains and long-running transactions.

When an application stalls on PostgreSQL locks, the first questions are always
the same: which sessions are stuck, who is holding them up, where does the
chain start, and which transaction has been open far too long? LockLens answers
them from the command line, and can save what it saw so you can analyse the
incident after it is over.

LockLens **only observes**. It runs read-only queries in read-only transactions
and has no command to cancel queries, terminate backends, or change the
database.

## What it answers

| Question | Where to look |
| --- | --- |
| Which sessions are blocked? | `Blocked sessions`, `analysis.nodes[].blocked_by` |
| Which sessions block others? | `Blocking chains`, `analysis.nodes[].blocks` |
| Who is the root blocker of a chain? | `Incident N: root blocker ...`, `analysis.components[].roots` |
| How many sessions does one session block, directly and transitively? | `blocks X directly, Y in total`, `direct_blocked_count` / `transitive_blocked_count` |
| Which transactions have been open long, especially `idle in transaction`? | `Long-running transactions`, `analysis.long_transactions` |
| Can I review the state after the incident? | `locklens snapshot save` / `locklens snapshot show` |

Blocking relations come from PostgreSQL's own `pg_blocking_pids()`, not from
guessing conflicts in `pg_locks`. See
[docs/postgresql-observation.md](docs/postgresql-observation.md).

## Requirements

- Go 1.26 or later to build from source (developed with Go 1.26.0; unit tests
  also pass with Go 1.27.3; CI is configured for Go 1.26.x and 1.27.x).
- PostgreSQL 14 or later on the server you observe. The integration suite was
  run locally and passed against PostgreSQL 14.24, 15.19, 16.15, 17.11, and
  18.6; see [docs/testing.md](docs/testing.md#postgresql-versions).
- Docker with Docker Compose v2, only for the demo and integration tests.

## Quick start from source

```bash
git clone <this repository> locklens && cd locklens
make build            # produces bin/locklens and bin/locklens-demo
```

Point LockLens at a server. Prefer the environment variable: a `--dsn` on the
command line is visible to other local users in the process list.

```bash
export LOCKLENS_DSN='postgres://observer@db.example.internal:5432/app?sslmode=verify-full'
bin/locklens doctor
bin/locklens inspect
```

## Demo in a few minutes

The demo starts a disposable PostgreSQL in Docker (bound to `127.0.0.1:55432`,
fake local credentials), creates **real** lock contention with separate
connections, runs `locklens inspect` against it, and rolls everything back.

```bash
make demo           # build, start PostgreSQL, run all three scenarios
```

Or one scenario at a time:

```bash
make demo-row-lock  # Demo A: A updates a row and stays open, B blocks: A -> B
make demo-chain     # Demo B: A holds row 1; B holds row 2 and waits for row 1; C waits for row 2: A -> B -> C
make demo-idle      # Demo C: a session stays idle in transaction
```

To explore a scenario interactively, keep it running and watch it from a
second terminal:

```bash
bin/locklens-demo --hold chain
```

```bash
export LOCKLENS_DSN='postgres://locklens_observer:observer-pw@127.0.0.1:55432/locklens_demo?sslmode=disable'
bin/locklens watch --min-tx-age 1s
```

Press Ctrl+C in the first terminal to roll the scenario back, then stop the
database:

```bash
make db-down
```

The demo orchestrator synchronises by polling `pg_blocking_pids()` and
`pg_stat_activity` with a timeout, never by fixed sleeps. Its write code lives
in [`demo/`](demo/) and is never imported by the CLI.

## Commands

Full reference: [docs/cli-reference.md](docs/cli-reference.md).

```bash
locklens doctor                                   # connectivity, version, privileges, test collection
locklens inspect                                  # one report
locklens inspect --format json | jq '.analysis.summary'
locklens inspect --min-tx-age 30s --include-query --query-max-length 120
locklens watch                                    # refresh every 2s until Ctrl+C
locklens watch --format json > incident.ndjson    # one complete JSON report per line
locklens snapshot save --out incident.json        # save for later; never overwrites without --force
locklens snapshot show incident.json              # offline, no database needed
locklens version
```

Exit codes: `0` success (including when blocking was found), `1` runtime,
connection, or collection failure, `2` invalid flags, arguments, configuration,
or input file.

### Example output

Real output of `locklens inspect --min-tx-age 1s --include-query --query-max-length 60`
during `bin/locklens-demo --hold chain` (PIDs and times differ on every run):

```text
LockLens report
  Observed at:  2026-10-24 09:16:24 UTC (server clock)
  Target:       127.0.0.1:55432/locklens_demo as locklens_observer
  Server:       PostgreSQL 17.11
  Collection:   11 ms from several catalog reads (not an atomic snapshot)

Summary
  Blocked sessions:                2
  Blocking sessions:               2
  Root blockers:                   1
  Long transactions (>= 1s):       3
    of which idle in transaction:  1

Blocking chains (blocker -> waiter)
  Incident 1: root blocker pid 116 blocks 1 directly, 2 in total
    pid 116  idle in transaction  tx 3s  user=demo_app  app=locklens-demo-A  blocks 1 directly, 2 in total
    └─ pid 117  active  lock wait 3s  tx 3s  user=demo_app  app=locklens-demo-B  blocks 1 directly, 1 in total
          waiting for ShareLock on transaction 746 (typically a row lock held by that transaction)
       └─ pid 118  active  lock wait 3s  tx 3s  user=demo_app  app=locklens-demo-C
             waiting for ShareLock on transaction 747 (typically a row lock held by that transaction)

Blocked sessions
  PID  BLOCKED BY  STATE   WAIT EVENT          LOCK WAIT  TX AGE  QUERY AGE  USER      APPLICATION
  117  116         active  Lock:transactionid  3s         3s      3s         demo_app  locklens-demo-B
  118  117         active  Lock:transactionid  3s         3s      3s         demo_app  locklens-demo-C
  QUERY AGE is the age of the current statement, not the lock wait.

Long-running transactions (>= 1s)
  PID  STATE                    TX AGE  IN STATE FOR  BLOCKS  USER      DATABASE       APPLICATION      CLIENT
  116  idle in transaction (!)  3s      3s            1       demo_app  locklens_demo  locklens-demo-A  192.168.158.1
  117  active                   3s      3s            1       demo_app  locklens_demo  locklens-demo-B  192.168.158.1
  118  active                   3s      3s            -       demo_app  locklens_demo  locklens-demo-C  192.168.158.1
  (!) idle in transaction: the transaction is open but no statement is running.

Query text (opt-in, up to 60 characters each)
  pid 116: UPDATE "demo_accounts" SET balance = balance + 1 WHERE id =  [truncated]
  pid 117: UPDATE "demo_accounts" SET balance = balance + 1 WHERE id =  [truncated]
  pid 118: UPDATE "demo_accounts" SET balance = balance + 1 WHERE id =  [truncated]
```

## PostgreSQL privileges

LockLens does **not** need a superuser.

- **Minimum:** any role that can log in and connect to a database. Every role
  can read `pg_locks` and `pg_prepared_xacts` and execute
  `pg_blocking_pids()`, so blocking relations are always visible. Details of
  other roles' sessions (state, timings, query text) are hidden, and the report
  says so with a `restricted_visibility` warning.
- **Recommended:** grant `pg_read_all_stats` (or `pg_monitor`, which includes
  it) for a complete report:

  ```sql
  CREATE ROLE locklens_observer LOGIN PASSWORD '...';
  GRANT pg_read_all_stats TO locklens_observer;
  ```

`locklens doctor` reports which case applies. Details:
[docs/postgresql-observation.md](docs/postgresql-observation.md#required-privileges).

## Protecting query text and credentials

- Query text is **not collected** unless you pass `--include-query`; then it is
  cut to `--query-max-length` characters (default 200), counted in runes so
  UTF-8 is never split.
- The DSN, passwords, and other credentials are never written to output,
  snapshots, or logs. Errors pass through a redactor that removes the exact
  password and DSN values (including URL-encoded forms), and connection strings
  that fail to parse are never echoed at all.
- Every server-provided string is neutralised before it reaches your terminal,
  so a client cannot inject escape sequences through `application_name` or
  query text.
- TLS settings (`sslmode`, `sslrootcert`, ...) are honoured exactly as pgx and
  libpq define them; LockLens never weakens them.
- Snapshot files are created with mode `0600`.

See [SECURITY.md](SECURITY.md).

## Snapshot format

`snapshot save` writes JSON with `"schema_version": 1`, the collection time in
UTC, the observed server (host, port, database, user, version; never
credentials), sessions, blocker → waiter edges, prepared transactions, and
warnings. `snapshot show` re-derives the analysis from that raw data, so the
offline report matches the live one. Field reference, nullability, and
compatibility rules: [docs/snapshot-format.md](docs/snapshot-format.md).
A real example: [docs/examples/snapshot-chain.json](docs/examples/snapshot-chain.json).

## Overhead and observation limits

- One collection runs five catalog queries plus a clock read in one read-only
  transaction, using a pool of at most two connections. `pg_blocking_pids()` is
  called only for backends that actually wait for a lock, because each call
  needs brief exclusive access to the lock manager's shared state.
- `watch` defaults to a 2-second interval and refuses intervals under 500 ms.
  Collections never overlap.
- A report is assembled from several catalog reads and is **not an atomic
  snapshot** of the server.
- Lock wait age comes from `pg_locks.waitstart`; it is shown as `unknown` when
  PostgreSQL has not recorded it. `query_start` and `state_change` are never
  used as a substitute.

All limitations: [docs/limitations.md](docs/limitations.md).

## Blocking is not deadlock

A blocking chain means sessions are waiting; it resolves when the root blocker
commits or rolls back. A deadlock is a cycle of waits that can never resolve on
its own; PostgreSQL detects it after `deadlock_timeout` and aborts one of the
transactions. LockLens labels a cycle in its observed data as a cycle and does
not invent a root blocker for it, but it never claims a confirmed deadlock: its
data is not atomic, and the server's deadlock detector is the authority.

## Documentation

- [docs/cli-reference.md](docs/cli-reference.md): commands, flags, defaults, exit codes, output formats
- [docs/postgresql-observation.md](docs/postgresql-observation.md): views, fields, privileges, versions, semantics
- [docs/blocking-graph.md](docs/blocking-graph.md): graph model, counting, cycles, complexity
- [docs/snapshot-format.md](docs/snapshot-format.md): JSON schema and compatibility
- [docs/architecture.md](docs/architecture.md): components and data flow
- [docs/limitations.md](docs/limitations.md): what LockLens cannot see or guarantee
- [docs/troubleshooting.md](docs/troubleshooting.md): connection, permission, TLS, Docker, snapshot errors
- [docs/development.md](docs/development.md): building, checks, adding features
- [docs/testing.md](docs/testing.md): test strategy and tested PostgreSQL versions
- [docs/implementation-plan.md](docs/implementation-plan.md): implementation checklist
- [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md), [LICENSE](LICENSE)

## Roadmap

Not implemented yet; ideas under consideration:

- Optional enrichment of the lock a blocker holds, for display only, without
  changing how blocking relations are determined.
- A `snapshot diff` command to compare two saved snapshots.
- Packaged release binaries.

## License

MIT. See [LICENSE](LICENSE).
