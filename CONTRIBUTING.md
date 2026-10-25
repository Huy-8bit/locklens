# Contributing to LockLens

Thank you for helping. LockLens is a read-only PostgreSQL monitoring and
incident investigation toolkit: a CLI and a server with a web UI.
Contributions that keep it accurate, safe, and easy to reason about are very
welcome.

## Ground rules

- **Observation only.** Features that cancel queries, terminate backends,
  change configuration, create or drop indexes, run `VACUUM`, fail over, or
  otherwise modify a monitored database are out of scope.
- **Evidence, not guesses.** Rules report observations with evidence and
  limitations; possible explanations are never presented as confirmed causes.
  Missing data produces "insufficient data", never a pass. Unknown values are
  null, never zero.
- **Accuracy over guesses.** Blocking relations come from `pg_blocking_pids()`.
  Do not infer conflicts from `pg_locks`, and do not present derived values
  (for example lock wait time) without a source that supports them. Link the
  official PostgreSQL documentation for any new semantics.
- **No credential or query-text leaks.** Never print, log, return, or export a
  DSN or any part of an unparsable DSN. SQL text collection stays switchable per target, row browsing stays opt-in, and SQL text
  never goes into metric labels.
- **No mock data in production code.** UI elements show real data or explicit
  empty, partial, or unavailable states.
- **English** for code, comments, UI and CLI text, tests, and documentation.

## Workflow

1. Open an issue for anything larger than a small fix, so the approach can be
   agreed first.
2. Read [docs/development.md](docs/development.md) for setup, layout, and the
   "adding a feature" checklist, and [docs/architecture.md](docs/architecture.md)
   for package boundaries.
3. Make the change together with its tests and documentation. Documentation is
   part of the change, not a follow-up: update
   [docs/cli-reference.md](docs/cli-reference.md),
   [docs/snapshot-format.md](docs/snapshot-format.md), and the other affected
   documents, and remove anything that is no longer true.
4. Run the checks:

   ```bash
   make check                # Go formatting, vet, unit and race tests
   make web-check            # frontend typecheck, lint, unit tests
   make test-integration     # isolated Docker Compose projects, removed afterwards
   make e2e                  # browser tests against the Docker stack
   ```

   If you cannot run some of them, say which in the pull request.
5. Describe in the pull request what you changed, why, and how you verified it.

## Style

- `gofmt`, `go vet`, idiomatic Go names.
- Doc comments on exported identifiers; comments explain *why*, not *what*.
- SQL in `backend/internal/lib/postgres/sql/` or next to its collector or store code,
  with a comment explaining assumptions and version requirements. Schema
  changes are new files in `backend/migrations/`; applied migrations are never edited.
- Frontend: TypeScript strict mode, ESLint clean, accessible labels and
  keyboard support, state in the URL where it should be shareable.
- Tests assert on parsed data, not on whole output strings containing PIDs or
  timestamps.
- Keep dependencies minimal; justify any new module in the pull request.

## Commit messages

Use the imperative mood and a short summary line, for example
`graph: count shared descendants once`.

## Reporting security issues

Please do not open public issues for vulnerabilities. See
[SECURITY.md](SECURITY.md).
