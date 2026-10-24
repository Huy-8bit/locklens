# Development

## Setup

Requirements:

- Go 1.26 or later. With an older local Go and the default `GOTOOLCHAIN=auto`,
  the go command downloads the toolchain required by `go.mod` automatically.
- Docker with Docker Compose v2 for the demo and integration tests.
- GNU or BSD `make`.

```bash
make build      # bin/locklens and bin/locklens-demo
make help       # list all targets
```

`make build` injects version metadata (`git describe`, commit, UTC build time)
with `-ldflags`. A plain `go build ./cmd/locklens` works too; `version` then
falls back to Go's embedded VCS data.

## Repository layout

```text
cmd/locklens/            main package: calls cli.Main
internal/cli/            commands, flags, exit codes, watch loop
internal/config/         DSN resolution, pool settings, credential redaction
internal/postgres/       collector, doctor, error hints
internal/postgres/sql/   one commented SQL file per query (embedded)
internal/model/          snapshot schema types
internal/snapshot/       snapshot file I/O
internal/graph/          blocking graph algorithms
internal/report/         analysis built from a snapshot
internal/render/         table and JSON output
internal/textsafe/       UTF-8 truncation, terminal sanitising
internal/buildinfo/      version metadata
demo/scenario/           creates real lock contention (writes!)
demo/cmd/locklens-demo/  demo orchestrator
demo/initdb/             roles created when the demo database starts
test/integration/        integration tests (build tag "integration")
docs/                    documentation; docs/examples holds a real snapshot
```

[architecture.md](architecture.md) explains how the packages fit together.

## Everyday commands

| Task | Command |
| --- | --- |
| Format | `make fmt` |
| Check formatting | `make fmt-check` |
| Static checks | `make vet` (runs `go vet` with and without the `integration` tag) |
| CLI does not import demo code | `make check-separation` |
| Unit tests | `make test` |
| Unit tests with race detector | `make test-race` |
| All checks without a database | `make check` |
| Start / stop the demo database | `make db-up` / `make db-down` |
| Integration tests | `make db-up && make test-integration` |
| Demo | `make demo` |
| Doctor against the demo database | `make doctor` |

Use another PostgreSQL major version for the demo database with
`LOCKLENS_PG_VERSION`, for example `make db-up LOCKLENS_PG_VERSION=14`.
`make db-up` passes `--renew-anon-volumes`, so each start begins with an empty
database and switching versions never reuses an incompatible data directory.

The host port defaults to `55432`; change it with `LOCKLENS_PG_PORT` and set
the `LOCKLENS_IT_*_DSN` variables accordingly (see
[testing.md](testing.md#configuration)).

## Conventions

- All repository content is in English.
- Reports go to stdout; logs and errors go to stderr.
- Any string from the server is rendered through `textsafe.Terminal`.
- Never include a DSN or any part of an unparsable DSN in an error.
- New SQL goes into `internal/postgres/sql/` with a header comment explaining
  assumptions and version requirements. Avoid per-session queries.
- Comments explain *why*; do not restate the code.

## Adding a feature

1. **Write down the behaviour** in the relevant document first (CLI flags in
   [cli-reference.md](cli-reference.md), new fields in
   [snapshot-format.md](snapshot-format.md), observation semantics in
   [postgresql-observation.md](postgresql-observation.md)) and add a pending
   item to [implementation-plan.md](implementation-plan.md).
2. **Check PostgreSQL semantics** in the official documentation before relying
   on a column or function, and link the source in the docs.
3. **Implement** in the right layer: collection in `postgres`, raw fields in
   `model`, derived values in `report`, presentation in `render`.
4. **Snapshot compatibility:** a new optional field keeps `schema_version`; a
   removed, renamed, or re-typed field requires a version bump and a decision
   about reading old files. Regenerate `docs/examples/snapshot-chain.json` if
   the serialised shape changes (`TestDocumentedExampleMatchesSerializer` will
   fail until docs and serializer agree).
5. **Test:** unit tests for logic, integration tests for anything that depends
   on real PostgreSQL behaviour. Assert on parsed JSON and PIDs reported by the
   scenario, never on full output strings.
6. **Update docs and the plan** in the same change; mark the item done only
   when implementation, checks, and documentation are complete.

## Regenerating the documented example snapshot

```bash
make build db-up
bin/locklens-demo --hold chain &
export LOCKLENS_DSN='postgres://locklens_observer:observer-pw@127.0.0.1:55432/locklens_demo?sslmode=disable'
bin/locklens snapshot save --out docs/examples/snapshot-chain.json --min-tx-age 1s --force
kill -INT %1
```

Then paste the file into the example block of
[snapshot-format.md](snapshot-format.md) and update the PIDs quoted in
`internal/cli/cli_test.go` (`TestSnapshotShowDocumentedExample`).
