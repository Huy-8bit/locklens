# Testing

## Strategy

| Layer | Where | Needs a database | What it proves |
| --- | --- | --- | --- |
| Graph algorithms | `internal/graph` | no | Counts, roots, cycles, duplicates, determinism |
| Analysis | `internal/report` | no | Node kinds, the three ages, thresholds, NULL handling, warnings |
| Rendering | `internal/render` | no | Sanitising, shared nodes, cycles terminate, query opt-in, NDJSON lines |
| Text safety | `internal/textsafe` | no | Rune truncation, control and bidi characters |
| Configuration | `internal/config` | no | DSN precedence, observer settings, redaction, unparsable DSNs never echoed |
| Snapshot files | `internal/snapshot` | no | Round trip, no-clobber, atomic writes, versions, corrupt files, docs example |
| CLI behaviour | `internal/cli` | no | Exit codes, offline `snapshot show`, `watch` retries and clean cancel |
| Error hints | `internal/postgres` | no | SQLSTATE classification for hints and fatal errors |
| End to end | `test/integration` | yes | Real contention observed through the built binary |

### Unit test scenarios

- Graph: chain, fan-out, multiple blockers, shared descendants counted once,
  duplicate edges, PID 0, two-node cycle, cycle fed by an unblocked entry,
  self loop, component ordering, a 2,000-node cycle (no stack overflow),
  deterministic output.
- Analysis: missing and prepared-transaction nodes, cycle warning wording
  ("not a confirmed deadlock"), all-NULL timestamps yield unknown ages, lock
  wait stays unknown without `waitstart`, threshold boundary is inclusive,
  only client backends count as long transactions, aborted idle transactions,
  clamping of future timestamps.
- Redaction: password in URL and key/value DSNs, URL-encoded variants,
  `PGPASSWORD`, wrapped errors; four malformed DSNs whose errors must not
  contain any fragment of the input.
- Text: multi-byte and emoji truncation, invalid UTF-8 repair, ESC/OSC/CSI,
  C1 controls, DEL, bidi overrides.
- CLI: twelve invalid invocations exit 2; connection refused exits 1 with a
  hint and no password; `doctor` JSON on connection failure; `snapshot save`
  refuses an existing file before connecting; `watch` keeps retrying a closed
  port and exits 0 on cancellation without printing an error.

### Integration scenarios

Each test creates contention with [`demo/scenario`](../demo/scenario/scenario.go),
runs the compiled `locklens` binary, and asserts on parsed JSON using the PIDs
the scenario reports:

| Test | Checks |
| --- | --- |
| `TestRowLock` | Edge A → B, A is root with direct 1 / transitive 1, B waits on `transactionid`, `wait_event_type = Lock`, lock wait age known |
| `TestChain` | A → B → C, no spurious A → C, counts 2/1/0, single acyclic component rooted at A |
| `TestIdleInTransaction` | Flagged idle in transaction; `xact_start` equals `now()` inside the transaction to the microsecond; reported age equals `server_time − xact_start`; threshold respected |
| `TestNoBlocking` | `No blocking detected`, exit 0 |
| `TestOwnConnectionsExcluded` | LockLens' own collecting session never appears |
| `TestQueryTextOptIn` | No query text in JSON, table, or snapshot by default; with `--include-query --query-max-length 10` exactly 10 characters and `query_truncated` |
| `TestSnapshotSaveAndShowOffline` | Save, end the scenario, show with an unreachable DSN in the environment; no credentials in the file; mode 0600; overwrite refused without `--force` (exit 2) and allowed with it |
| `TestRestrictedRoleDegradesExplicitly` | A plain role still sees the edge but no details, gets `restricted_visibility`, a `[details hidden by permissions]` marker, and a doctor warning |
| `TestPreparedTransactionBlocker` | Edge 0 → B, PID 0 node of kind `prepared_transaction`, GID listed, warning raised |
| `TestDoctor` | All checks `ok` for the observer role |
| `TestAuthenticationFailure` | Exit 1, authentication hint, wrong password not echoed |
| `TestWatchNDJSONAndInterrupt` | Three NDJSON lines, each a complete report; SIGINT exits 0 with empty stderr |
| `TestWatchTableWithoutTTYHasNoEscapeCodes` | Piped table output has frame separators and no escape codes |

## Synchronisation and cleanup

- **No fixed sleeps for synchronisation.** After starting a statement that
  should block, the scenario polls `pg_blocking_pids(waiter)` every 25 ms until
  it contains the expected blockers; the idle scenario polls
  `pg_stat_activity` until the session is `idle in transaction` and its
  transaction is old enough by the server clock. Polling gives up after 15 s.
- **Cleanup order.** Sessions are released in creation order: rolling back the
  holder lets the waiter's statement finish, which is then rolled back, which
  releases the next waiter. A statement that does not finish within 5 s is
  cancelled, and closing the connection makes the server abort anything left.
  Prepared transactions are rolled back with `ROLLBACK PREPARED`.
- **Server-side safety nets.** Demo sessions set `lock_timeout = 5min` and
  `idle_in_transaction_session_timeout = 10min`, so even a killed test process
  cannot leave locks behind for long.
- **Isolation and repeatability.** Each test uses its own table
  (`it_<test name>`), recreated on every run. Tests run sequentially within the
  package and use `-count=1` to bypass the test cache. Every test has a 60 s
  context; the suite runs with `-timeout 5m`.

## Reproducing the demos by hand

```bash
make build db-up
bin/locklens-demo row-lock        # or chain, idle, all
bin/locklens-demo --hold chain    # keep it running; Ctrl+C rolls back
bin/locklens-demo --format json --idle-age 5s idle
make db-down
```

`bin/locklens-demo -h` lists all flags. The orchestrator passes the observer DSN
to `locklens` through `LOCKLENS_DSN`, not on the command line.

## Running the tests

```bash
make check                 # gofmt check, go vet, separation check, unit tests, race detector
make db-up                 # PostgreSQL 17 by default
make test-integration      # integration tests with -race
make db-up LOCKLENS_PG_VERSION=14 && make test-integration
make db-down
```

### Configuration

Integration tests connect with these defaults, matching `docker-compose.yml`
and `demo/initdb/01-roles.sql`:

| Variable | Default |
| --- | --- |
| `LOCKLENS_IT_DEMO_DSN` | `postgres://demo_app:demo-app-pw@127.0.0.1:55432/locklens_demo?sslmode=disable` |
| `LOCKLENS_IT_OBSERVER_DSN` | `postgres://locklens_observer:observer-pw@127.0.0.1:55432/locklens_demo?sslmode=disable` |
| `LOCKLENS_IT_LIMITED_DSN` | `postgres://locklens_limited:limited-pw@127.0.0.1:55432/locklens_demo?sslmode=disable` |

The prepared-transaction test needs `max_prepared_transactions > 0`, which the
compose file sets.

## PostgreSQL versions

Versions on which the full integration suite was **run and passed locally**
(macOS arm64, Docker 29.8.2 via OrbStack, Docker Compose 5.5.0, Go 1.26.0,
official `postgres:<major>-alpine` images, 2026-10-24). Unit tests and
`go vet` were additionally run with Go 1.27.3.


| Major | Exact version | Result |
| --- | --- | --- |
| 14 | 14.24 | pass |
| 15 | 15.19 | pass |
| 16 | 16.15 | pass |
| 17 | 17.11 | pass |
| 18 | 18.6 | pass |

The GitHub Actions workflow ([`.github/workflows/ci.yml`](../.github/workflows/ci.yml))
is configured to run the same suite against majors 14, 15, 16, 17, and 18
(the latest image of each major at run time). It has not been run as part of
this work because nothing has been pushed; check the workflow history once the
repository is published.
