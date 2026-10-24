# Contributing to LockLens

Thank you for helping. LockLens is small on purpose: a read-only CLI that
reports PostgreSQL lock contention accurately. Contributions that keep it
accurate, safe, and easy to reason about are very welcome.

## Ground rules

- **Observation only.** Features that cancel queries, terminate backends, or
  modify the database are out of scope.
- **Accuracy over guesses.** Blocking relations come from `pg_blocking_pids()`.
  Do not infer conflicts from `pg_locks`, and do not present derived values
  (for example lock wait time) without a source that supports them. Link the
  official PostgreSQL documentation for any new semantics.
- **No credential or query-text leaks.** Never print a DSN or any part of an
  unparsable DSN. Query text stays opt-in.
- **English** for code, comments, CLI text, tests, and documentation.

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
   make check
   make db-up && make test-integration && make db-down
   ```

   If you cannot run the integration tests, say so in the pull request.
5. Describe in the pull request what you changed, why, and how you verified it.

## Style

- `gofmt`, `go vet`, idiomatic Go names.
- Doc comments on exported identifiers; comments explain *why*, not *what*.
- SQL in `internal/postgres/sql/`, one file per query, with a header comment.
- Tests assert on parsed data, not on whole output strings containing PIDs or
  timestamps.
- Keep dependencies minimal; justify any new module in the pull request.

## Commit messages

Use the imperative mood and a short summary line, for example
`graph: count shared descendants once`.

## Reporting security issues

Please do not open public issues for vulnerabilities. See
[SECURITY.md](SECURITY.md).
