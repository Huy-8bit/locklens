# Implementation plan

Live checklist for the LockLens MVP. Status markers:

- `[x]` done: implemented, checked, and documented
- `[ ]` pending
- `[!]` blocked (reason given)

## Foundations

- [x] Go module (`go 1.26.0`), dependencies limited to `pgx/v5` and `cobra`, Makefile, `.gitignore`, `.env.example`
- [x] Build metadata (`version`, `commit`, `date`) injected with `-ldflags`, VCS fallback
- [x] PostgreSQL semantics checked against the official docs before writing SQL:
      `pg_blocking_pids` (hard/soft blocks, parallel duplicates, PID 0),
      `pg_locks.waitstart` (PostgreSQL 14+, may be briefly NULL),
      `pg_stat_activity` visibility rules and timestamp meanings,
      `pg_monitor` membership. Sources linked in docs/postgresql-observation.md

## Core logic (no database)

- [x] `internal/model`: snapshot schema v1 (raw data only)
- [x] `internal/graph`: blocker → waiter graph, deduplication, direct/transitive counts, roots, iterative Tarjan cycle detection, components
- [x] `internal/report`: node kinds, transaction/query/state/lock-wait ages, long transactions, analysis warnings
- [x] `internal/textsafe`: rune-safe truncation, control/bidi neutralisation
- [x] `internal/config`: DSN precedence, observer runtime parameters, exact-value redaction, unparsable DSNs never echoed
- [x] `internal/snapshot`: atomic no-clobber save (hard link), `--force` rename, mode 0600, versioned load with clear errors

## PostgreSQL collection

- [x] Read-only pool (max 2 connections; `application_name=locklens`, `default_transaction_read_only=on`, `statement_timeout`, `lock_timeout`, `idle_in_transaction_session_timeout`)
- [x] Own backends excluded by PID captured in pool hooks
- [x] Edges only from `pg_blocking_pids`, called once per waiting backend found in ungranted `pg_locks` rows; parallel workers folded into leaders; duplicates removed
- [x] Session metadata from `pg_stat_activity`; query text only with `--include-query`, cut on the server and again by runes
- [x] Waiting-lock description and `waitstart`; relation names only for the current database
- [x] PID 0 kept as a `prepared_transaction` node; `pg_prepared_xacts` listed as context
- [x] `missing` placeholders for PIDs that vanished between reads
- [x] Visibility detection (`pg_has_role`), `restricted_visibility` and `track_activities_off` warnings
- [x] Server clock read after all sources; ages clamped at zero
- [x] Versions below 14 rejected explicitly

## CLI

- [x] `doctor` (connection, version, source access, visibility, `track_activities`, query text, test collection; table/JSON)
- [x] `inspect`
- [x] `watch` (TTY-only screen clearing, NDJSON, sequential collections, bounded exponential backoff, fatal-error stop, clean Ctrl+C/SIGTERM)
- [x] `snapshot save` / `snapshot show`
- [x] `version`
- [x] Exit codes 0 / 1 / 2, errors on stderr with hints

## Demo and tests

- [x] Docker Compose PostgreSQL (`LOCKLENS_PG_VERSION`, port 55432 on loopback, `max_prepared_transactions`) with demo roles
- [x] Demo orchestrator (`demo/`): row lock, chain, idle in transaction; polling with timeout; ordered cleanup; `--hold`
- [x] CLI/demo separation enforced by `make check-separation`
- [x] Unit tests (graph, report, render, textsafe, config, snapshot, cli, postgres hints)
- [x] Integration tests against real PostgreSQL (13 tests)
- [x] GitHub Actions workflow (Go 1.26.x/1.27.x checks; PostgreSQL 14–18 integration matrix)

## Documentation

- [x] README.md
- [x] docs/architecture.md
- [x] docs/postgresql-observation.md
- [x] docs/blocking-graph.md
- [x] docs/cli-reference.md
- [x] docs/snapshot-format.md (example is a real snapshot, verified by a unit test)
- [x] docs/development.md
- [x] docs/testing.md
- [x] docs/troubleshooting.md
- [x] docs/limitations.md
- [x] CONTRIBUTING.md, SECURITY.md, LICENSE (MIT)

## Verification record (2026-10-24, macOS arm64)

- [x] `gofmt -l .` empty; `go vet ./...` and `go vet -tags integration ./...` clean
- [x] `go test ./...` and `go test -race ./...` pass (Go 1.26.0); `go vet` and `go test ./...` also pass with Go 1.27.3
- [x] Integration suite with `-race` passes on PostgreSQL 14.24, 15.19, 16.15, 17.11, 18.6 (Docker images `postgres:<major>-alpine`)
- [x] From a clean copy of the tree: `make demo` (fresh database) shows A → B, A → B → C with counts 1/2, and idle in transaction; `make check` passes
- [x] `snapshot save` during the chain demo, database stopped with `docker compose stop`, then `snapshot show` (table and JSON) renders the chain offline; `inspect` with the database stopped exits 1 with a hint
- [x] `watch` under a pseudo-terminal (`script`) clears the screen per frame and exits 0 on SIGINT with no error output; piped table and NDJSON output verified by integration tests
- [x] Documentation checks: repository-relative links and anchors (script), external PostgreSQL doc URLs and anchors (HTTP 200, anchors present), every flag and default in docs/cli-reference.md compared with `--help`, JSON code blocks parse, the snapshot example decodes with unknown fields disallowed and re-encodes identically, no non-English project text
- [!] GitHub Actions has not run: nothing was pushed (out of scope by instruction). The workflow file is untested on GitHub's runners

## Pending (roadmap, not part of the MVP)

- [ ] Display-only enrichment of the lock a blocker holds
- [ ] `snapshot diff`
- [ ] Release binaries
