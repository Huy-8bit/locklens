# Development

## Setup

Requirements:

- Go 1.26 or later. With an older local Go and the default `GOTOOLCHAIN=auto`,
  the go command downloads the toolchain required by `backend/go.mod`.
- Node.js 24.15+ (or 22.22.2+) with npm, for the web UI (CI uses Node.js 24).
- Docker with Docker Compose v2 for the lab, integration tests, and E2E tests.
- GNU or BSD `make`, Bash, Python 3, and curl for the development/test scripts.

```bash
make dev        # start the lab databases, local Go API (:18080), and Vite (:5173)
make build      # install/build React, embed assets, build all binaries in bin/
make test       # backend unit tests and frontend typecheck, lint, unit tests
make help       # list all targets
```

`make backend-build` builds only Go binaries, using the last UI build. On a
fresh checkout it serves the API without the UI. `make build` always builds
React before compiling Go. Assets are copied to
`backend/internal/webui/dist/`, inside the package containing `go:embed`;
generated assets are ignored. The tracked `README.txt` keeps the embed
pattern valid before the first frontend build. Migrations remain embedded
inside `backend/migrations/`.

## Repository layout

```text
backend/
  cmd/locklens/          CLI and server entrypoint
  cmd/locklens-demo/     CLI demo runner
  cmd/locklens-lab/      lab scenario runner
  internal/             application packages and colocated unit/integration tests
  migrations/           embedded monitoring store SQL migrations
  demo/scenario/        contention helpers used only by demos and tests
  demo/lab/             lab scenario implementation
  go.mod, go.sum        Go application module and dependency lockfile
web/
  src/app/              application entrypoint, routing, authentication
  src/features/         product pages and their tests
  src/components/       shared UI components and their tests
  src/api/              API client, hooks, and shared response types
  src/lib/              shared helpers and their tests
  src/styles/           application styles
  src/test/             frontend test setup and utilities
  public/               static public assets
  package.json          React toolchain and scripts
  package-lock.json     frontend dependency lockfile
deploy/                 Dockerfile, lab Compose, isolated test Compose
demo/                   PostgreSQL initialization and bootstrap target configuration
scripts/                development, integration, E2E, smoke, and lab scripts
tests/e2e/
  cli/, server/         cross-system Go tests (integration build tag)
  browser/              Playwright browser flows
  go.mod, go.sum        test module; local replacement points to backend/
  package.json          isolated Playwright toolchain
docs/                   documentation and examples
.github/workflows/      build and test automation
```

No root Go module or workspace is required. Application imports keep the
module path `github.com/locklens/locklens`; moving the module's physical
location does not change its API. Run Go commands with `go -C backend ...`.
The integration harness creates a temporary Go workspace to test backend
packages and the separate E2E module together, serially against disposable
databases. Package-level PostgreSQL integration tests stay beside their code;
only tests spanning the CLI, HTTP server, or browser live in `tests/e2e/`.
Static assets such as the favicon live in `web/public/`.

[architecture.md](architecture.md) explains how the packages fit together.

## Everyday commands

| Task | Command |
| --- | --- |
| Format / check formatting | `make fmt` / `make fmt-check` |
| Static checks | `make vet` (with and without the `integration` tag) |
| `locklens` does not import demo code | `make check-separation` |
| Go unit tests / with race detector | `make backend-test` / `make test-race` |
| All Go checks without a database | `make check` |
| Frontend typecheck, lint, unit tests | `make web-check` |
| Integration tests (PostgreSQL 14.24 and 18.6) | `make test-integration` |
| Integration tests on every supported major | `make test-compat` |
| Browser E2E against the Docker stack | `make e2e` |
| Performance smoke check (lab must be up) | `./scripts/perf-smoke.sh` |
| Start / stop LockLens only (your databases) | `make up` / `make down` |
| Start / stop the demo lab | `make lab-up` / `make lab-down` |
| Run a lab scenario | `make lab SCENARIO=chain` |
| CLI demo against the lab primary | `make demo` |

### Running the server locally

`make dev` starts the databases, API, and Vite together. Stop it with Ctrl-C;
the databases remain available until `make lab-down`. Set `LOCKLENS_LISTEN` and
`LOCKLENS_DEV_WEB_PORT` to choose other API and Vite ports. For a manual API launch
against the lab databases (`make lab-up`, or only the databases with
`docker compose -f deploy/compose.yaml -f deploy/compose.lab.yaml --env-file deploy/lab.env up -d --wait store primary standby`):

```bash
make build
export LOCKLENS_STORE_DSN='postgres://locklens_store:lab-store-pw@127.0.0.1:55434/locklens?sslmode=disable'
export LOCKLENS_MASTER_KEY='bG9ja2xlbnMtbGFiLW9ubHktbm90LWEtcmVhbC1rZXk='   # fake lab key
export LOCKLENS_ADMIN_PASSWORD='locklens-lab-admin' LOCKLENS_DEMO_MODE=true
export LOCKLENS_LISTEN=127.0.0.1:18080        # the lab container already uses 8080
bin/locklens server
```

### Frontend development

```bash
cd web
npm ci
LOCKLENS_DEV_API=http://127.0.0.1:18080 npm run dev     # Vite on :5173, proxies /api to the server
npm run typecheck && npm run lint && npm test
```

The Vite proxy keeps the API same-origin, so cookies and CSRF behave as in
production. `make web` builds and copies the UI for embedding.

## Conventions

- All repository content is in English.
- CLI reports go to stdout; logs and errors go to stderr.
- Never include a DSN, password, or any part of an unparsable DSN in an error,
  log line, API response, or export.
- Never put query text or other unbounded values into metric labels.
- Strings from monitored servers are rendered through `textsafe.Terminal` in
  the CLI and escaped by React or `html/template` elsewhere.
- Collectors run only read-only SQL. Demo code that writes belongs in `backend/demo/`; fixtures stay in `demo/`.
- Unknown values are `NULL`/`null`, never `0`.
- New SQL goes into `backend/internal/postgres/sql/` or next to its collector with a
  comment explaining assumptions and version requirements.
- Frontend: no mock data in production code; every page has loading, empty,
  partial, and error states; time ranges and filters live in the URL.
- Comments explain *why*; do not restate the code.

## Adding a feature

1. **Write down the behaviour** in the relevant document first and add an item
   to [implementation-plan.md](implementation-plan.md).
2. **Check PostgreSQL semantics** in the official documentation before relying
   on a column or function, including the versions that have it.
3. **Implement** in the right layer:
   - a new metric: the collector that reads its source, a rate/delta definition
     if it is a counter, and [collectors.md](collectors.md#metrics);
   - a new rule: see [rules.md](rules.md#adding-a-rule);
   - a schema change: a new numbered file in `backend/migrations/` (never edit an
     applied migration), plus storage code and retention if it holds history;
   - an API field: handler, `web/src/api/types.ts`, and [api.md](api.md).
4. **Snapshot compatibility:** a new optional field keeps `schema_version`; a
   removed, renamed, or re-typed field requires a version bump. The same
   applies to the incident export format.
5. **Test:** unit tests for logic, integration tests for anything that depends
   on real PostgreSQL behaviour, component tests for UI states, and the E2E
   suite for user-visible flows. Assert on parsed JSON and IDs, never on full
   output strings.
6. **Update docs and the plan** in the same change.

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
`backend/internal/cli/cli_test.go` (`TestSnapshotShowDocumentedExample`).
