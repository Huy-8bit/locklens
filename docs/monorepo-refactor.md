# Monorepo refactor verification

Date: 2026-10-24.

## Layout and build behavior

- All Go application and demo implementation is in `backend/`, including
  the three command entrypoints, internal packages, embedded migrations,
  module manifest, and dependency lockfile.
- React lives in `web/`. Application wiring is in `src/app/`, product pages
  in `src/features/`, shared UI in `src/components/`, API access in `src/api/`,
  helpers in `src/lib/`, and styles in `src/styles/`.
- Deployment definitions live in `deploy/`; PostgreSQL fixtures and bootstrap
  target configuration remain in `demo/`.
- Unit tests remain beside the implementation. Package-level PostgreSQL
  integration tests remain in `backend/internal/`. CLI, HTTP server, and
  browser cross-system tests live in `tests/e2e/`, with separate Go and npm
  manifests. The Go test module uses a local replacement for `backend/`.
- `make build` builds React before compiling Go, copying generated assets to
  `backend/internal/webui/dist/`. Embedding uses only package-local paths.
  Generated assets are ignored. The tracked `README.txt` allows Go-only builds
  from a fresh checkout before building React.
- Root commands include `make dev`, `make build`, `make test`, `make demo`,
  `make check`, `make test-integration`, `make e2e`, and `make docker-smoke`.
  CI uses the relocated module, lockfiles, deployment definitions, and tests.

## Verification

| Check | Result |
| --- | --- |
| Go formatting, vet (including integration-tagged code in both modules), production/demo import separation | PASS |
| Backend unit tests and race detector | PASS |
| Backend build of all three binaries | PASS |
| Frontend typecheck, ESLint, unit and accessibility tests | PASS: 5 files, 17 tests |
| Frontend production build and embedding through `make build` | PASS |
| Root `make test` | PASS |
| Docker Compose validation and shell script syntax | PASS |
| `make docker-smoke` | PASS: image build, migrations/readiness, HTML and JavaScript assets, SPA routing, CLI, lab runner, non-root runtime |
| `make test-integration` | PASS: 52 tests on PostgreSQL 14.24 and 52 on 18.6, race detection enabled; no failures or skips |
| `make e2e` | PASS: Chromium investigation flow against an isolated Docker stack |
| `make dev` | PASS: local API, Vite, live API proxy, clean API shutdown |
| `make demo` | PASS: row-lock, chain, idle; isolated project with a nondefault PostgreSQL port |
| Staged source exported to a fresh directory, without generated UI assets | PASS: backend unit tests/build and E2E Go vet |
| Local documentation links | PASS |

The development, demo, smoke, integration, and browser runs use isolated
Compose project names and test ports. Cleanup removes only those projects.
The demo target now passes the configured PostgreSQL port to both its writer
and observer connections, and accepts the configured binary directory.

Local frontend checks passed on Node.js 24.7.0 with npm engine warnings from
existing dependencies requiring Node.js 22.22.2+ or 24.15.0+ (or 26+). The
documented development prerequisite reflects those dependency requirements.
Go unit/build checks used Go 1.26.0; the integration workspace selected a
compatible Go 1.26 toolchain automatically.

The full PostgreSQL compatibility matrix (15.19, 16.15, and 17.11 in addition
to the default 14.24 and 18.6) is configured in CI and `make test-compat`;
the three intermediate versions and the performance smoke check were not
rerun for this structural refactor. Remote CI results are separate from the
local checks reported here.

## Preservation and source review

There was no `AGENTS.md` in the repository or its parent directories.
Existing source files and the Git index were backed up outside the repository
before moving files. A path-mapped preservation audit found no missing
pre-existing files. Existing implementation changes in moved source files
were retained; unrelated local changes to `SECURITY.md` were kept outside
the refactor commit.

The staged tree was reviewed for whitespace errors, broken documentation
links, unexpected environment files, dependencies, binaries, build output,
and common real credential patterns. Generated UI assets, installed
packages, local environment files, binaries, and test reports remain ignored.
The lab credentials in examples and fixtures are explicitly fake, local-only
values. The documentation screenshots show the local lab and are intentional
source assets.
