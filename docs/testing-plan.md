# Testing plan

QA checklist for LockLens. Results of an actual run are recorded in
[test-report.md](test-report.md); the test strategy is described in
[testing.md](testing.md).

Status markers: `[x]` executed and verified, `[ ]` not yet executed,
`[!]` blocked (reason given). Last executed on 2026-10-24; see the report for
versions and numbers.

## 1. Inspection

- [x] Read README, go.mod, Makefile, Compose files, tests, and docs; list implemented commands
- [x] Confirm PostgreSQL semantics against the official docs where tests depend on them
      (`pg_blocking_pids` hard/soft blocks and PID 0, `pg_locks.waitstart`,
      `pg_stat_activity` visibility, `pg_read_all_stats` / `pg_monitor`)
- [x] Determine the latest stable PostgreSQL major from postgresql.org and pin image tags

## 2. Isolated test environment

- [x] Dedicated Compose file (`deploy/compose.test.yaml`), separate from the demo Compose file
- [x] Unique Compose project name per run and PostgreSQL version
- [x] Ports published on `127.0.0.1` only, host port assigned dynamically
- [x] Concrete image tags (`postgres:<major>.<minor>-alpine`)
- [x] Synthetic data and throwaway credentials generated per run
- [x] Health check; readiness waited for with a bounded timeout (no fixed sleep)
- [x] Prepared transactions enabled only in this environment
- [x] No `.env` file is read; explicit DSNs passed to every test process
- [x] Integration tests refuse to run without explicit DSNs
- [x] Teardown with `docker compose -p <project> down -v` only; no global prune

## 3. Build and static checks

- [x] `gofmt -l .` is empty
- [x] `go -C backend vet ./...` and `go -C backend vet -tags integration ./...`
- [x] `go -C backend test ./...`
- [x] `go -C backend test -race ./...`
- [x] Build `bin/locklens`; end-to-end tests run the built binary

## 4. Real PostgreSQL scenarios

- [x] A. Healthy database: doctor and inspect succeed, empty graph, valid table and JSON, own connections excluded
- [x] B. Row blocking A → B: relation confirmed with `pg_blocking_pids`, CLI reports it, counts, blocking disappears after A is released
- [x] C. Chain A → B → C: confirmed with PostgreSQL first, root and unique descendant counts
- [x] D. Fan-out: one blocker, several waiters, no lost or duplicated edges, no inflated counts
- [x] D2. Multiple blockers on one waiter (live)
- [x] D3. Lock queue (soft block) behind a pending `ACCESS EXCLUSIVE` request (live)
- [x] E. Idle in transaction: state, transaction age, threshold filtering; transaction age, query age, and lock wait age kept distinct; lock wait age equals `pg_locks.waitstart`, not `query_start`/`state_change`
- [x] F. Restricted permissions: plain role gets partial data with warnings; after `GRANT pg_read_all_stats`, and separately `pg_monitor`, full visibility
- [x] G. Prepared transaction blocker: PID 0 edge kept, node kind, explicit `ROLLBACK PREPARED` in cleanup
- [x] H. Sessions disappearing during repeated collections: no panic, valid JSON, documented behaviour (cached metadata; `missing` placeholders not reproducible live)
- [!] Parallel-worker lock waits on a live server: not reproduced (no deterministic way found); unit-level only
- [!] TLS connections: not exercised, test servers run without TLS
- [x] Graph edge cases (multiple blockers, duplicates, shared descendants, cycles) covered by unit tests; cycles never called deadlocks

## 5. CLI end to end (built binary, subprocess timeouts)

- [x] Help and version output
- [x] DSN precedence: `--dsn` over `LOCKLENS_DSN`
- [x] Missing DSN, invalid flags, invalid durations → exit 2
- [x] Connection refused → exit 1
- [x] Authentication failure → exit 1, no password in output
- [x] Connection timeout (silent TCP listener) → exit 1 within the timeout
- [x] Collection timeouts: `statement_timeout` and `lock_timeout` paths
- [x] Exit codes match the documentation
- [x] JSON only on stdout, diagnostics only on stderr
- [x] Unicode query text and terminal control characters rendered safely
- [x] Watch: several cycles, every NDJSON line a complete report, no escape codes when redirected, SIGINT exits 0 cleanly
- [x] Watch: temporary database outage → retries on stderr, recovery, frames resume
- [x] Watch: bounded connections (≤ 2) and no overlapping collections
- [x] Snapshots: save and validate, offline show with an unusable DSN, no overwrite without `--force`, malformed JSON, unsupported version, failed write leaves no destination or temporary file

## 6. Query text and credentials

- [x] Canary query text absent from default table, JSON, NDJSON, and snapshot files
- [x] Canary present only with `--include-query` and sufficient privileges
- [x] UTF-8 query truncation by runes
- [x] Canary passwords absent from stdout, stderr, snapshots, and errors, for URI and keyword/value DSNs

## 7. Observer overhead smoke check

- [x] Repeated collections on a quiet database: latency distribution
- [x] Connection count and goroutine count stable after warm-up
- [x] Results recorded with version, conditions, and sample count; not presented as a benchmark

## 8. Defects and documentation

- [x] Each defect reproduced, fixed, covered by a regression test, affected checks rerun
- [x] README, CLI reference, permissions, troubleshooting, testing docs updated
- [x] `docs/test-report.md` written
- [x] CI configured to run the same harness with bounded timeouts and a version matrix
  (later reduced to unit tests and the image build; the harness runs locally)
- [!] CI executed on GitHub: blocked, nothing has been pushed (out of scope)
- [x] Test containers and volumes removed by exact project name
