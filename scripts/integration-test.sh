#!/usr/bin/env bash
# Run the LockLens integration suite against disposable PostgreSQL servers.
#
# For each version in LOCKLENS_IT_PG_VERSIONS (default: "14.24 18.6") this
# script:
#   1. starts deploy/compose.test.yaml under a unique Compose project name with a
#      pinned image tag, a loopback-only dynamic host port, and generated
#      throwaway credentials (no .env file is read);
#   2. waits for the health check with a bounded timeout;
#   3. creates the test roles;
#   4. runs `go test -tags integration` with explicit DSNs in the environment;
#   5. removes the project with `docker compose -p <project> down -v`, also on
#      failure or interruption.
#
# Logs are written to $LOCKLENS_IT_ARTIFACTS (default: test-results/).
# Set LOCKLENS_IT_KEEP=1 to keep the containers for debugging; they are then
# listed at the end with the exact command to remove them.
# LOCKLENS_IT_PACKAGES and LOCKLENS_IT_RUN narrow the Go packages and the
# `go test -run` filter, e.g. LOCKLENS_IT_PACKAGES=./tests/e2e/server/...
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT=$(pwd)
VERSIONS=${LOCKLENS_IT_PG_VERSIONS:-"14.24 18.6"}
ARTIFACTS=${LOCKLENS_IT_ARTIFACTS:-"$ROOT/test-results"}
GO_TEST_TIMEOUT=${LOCKLENS_IT_GO_TIMEOUT:-15m}
PACKAGES=${LOCKLENS_IT_PACKAGES:-"./tests/e2e/cli/... ./backend/internal/postgres/... ./backend/internal/storage/... ./backend/internal/workspace/... ./backend/internal/onboard/... ./backend/internal/incidents/... ./tests/e2e/server/..."}
RUN_FILTER=${LOCKLENS_IT_RUN:-.}
WAIT_TIMEOUT=${LOCKLENS_IT_WAIT_TIMEOUT:-120}
COMPOSE_FILE="$ROOT/deploy/compose.test.yaml"
RUN_ID="$(date +%s)-$$"
mkdir -p "$ARTIFACTS"

PROJECTS=()
WORKDIRS=()

cleanup() {
  local status=$?
  for i in "${!PROJECTS[@]}"; do
    local project=${PROJECTS[$i]} envfile="${WORKDIRS[$i]}/compose.env"
    if [[ "${LOCKLENS_IT_KEEP:-0}" == "1" ]]; then
      echo "Kept: docker compose -p $project -f $COMPOSE_FILE --env-file $envfile down -v"
      continue
    fi
    docker compose -p "$project" -f "$COMPOSE_FILE" --env-file "$envfile" --profile replication down -v --remove-orphans >/dev/null 2>&1 || true
    rm -rf "${WORKDIRS[$i]}"
  done
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# Throwaway passwords with a recognisable prefix, so tests can search all
# output for them as canaries.
secret() { printf 'cnry%s' "$(od -An -N10 -tx1 /dev/urandom | tr -d ' \n')"; }

run_version() {
  local version=$1
  local project="locklens-it-${version//./-}-$RUN_ID"
  local workdir
  workdir=$(mktemp -d "${TMPDIR:-/tmp}/locklens-it.XXXXXX")
  GOWORK=off go -C "$workdir" work init "$ROOT/backend" "$ROOT/tests/e2e" || return 1
  PROJECTS+=("$project")
  WORKDIRS+=("$workdir")

  local admin_pw demo_pw observer_pw limited_pw repl_pw
  admin_pw=$(secret); demo_pw=$(secret); observer_pw=$(secret); limited_pw=$(secret); repl_pw=$(secret)
  local envfile="$workdir/compose.env"
  cat >"$envfile" <<ENV
LOCKLENS_IT_IMAGE=postgres:${version}-alpine
LOCKLENS_IT_ADMIN_USER=locklens_it_admin
LOCKLENS_IT_ADMIN_PASSWORD=${admin_pw}
LOCKLENS_IT_DATABASE=locklens_it
LOCKLENS_IT_REPLICATION_PASSWORD=${repl_pw}
ENV
  local compose=(docker compose -p "$project" -f "$COMPOSE_FILE" --env-file "$envfile")

  echo "==> PostgreSQL ${version}: starting Compose project ${project}"
  # This function runs in an `||` list, where `set -e` does not apply, so
  # every step checks its own status.
  "${compose[@]}" up -d --wait --wait-timeout "$WAIT_TIMEOUT" || return 1

  local hostport
  hostport=$("${compose[@]}" port postgres 5432) || return 1
  local port=${hostport##*:}
  local server_version
  server_version=$("${compose[@]}" exec -T postgres psql -U locklens_it_admin -d locklens_it -Atc 'SHOW server_version') || return 1
  echo "    listening on 127.0.0.1:${port}, server_version=${server_version}"

  # Roles used by the tests. demo_app writes; the others only observe.
  "${compose[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -q -U locklens_it_admin -d locklens_it \
    -v demo_pw="$demo_pw" -v observer_pw="$observer_pw" -v limited_pw="$limited_pw" -v repl_pw="$repl_pw" <<'SQL' || return 1
CREATE ROLE demo_app LOGIN PASSWORD :'demo_pw';
GRANT CREATE ON SCHEMA public TO demo_app;
CREATE ROLE locklens_observer LOGIN PASSWORD :'observer_pw';
GRANT pg_read_all_stats TO locklens_observer;
CREATE ROLE locklens_limited LOGIN PASSWORD :'limited_pw';
CREATE ROLE replicator LOGIN REPLICATION PASSWORD :'repl_pw';
CREATE DATABASE locklens_pgss;
SQL
  "${compose[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -q -U locklens_it_admin -d locklens_pgss \
    -c "CREATE EXTENSION pg_stat_statements; GRANT CREATE ON SCHEMA public TO demo_app;" || return 1
  # Replication for the standby profile, then the standby itself.
  "${compose[@]}" exec -T postgres sh -c 'echo "host replication replicator all scram-sha-256" >> "$PGDATA/pg_hba.conf"' || return 1
  "${compose[@]}" exec -T postgres psql -q -U locklens_it_admin -d locklens_it -c 'SELECT pg_reload_conf()' >/dev/null || return 1
  "${compose[@]}" --profile replication up -d --wait --wait-timeout "$WAIT_TIMEOUT" standby || return 1
  local sport
  sport=$("${compose[@]}" --profile replication port standby 5432) || return 1
  sport=${sport##*:}
  echo "    standby on 127.0.0.1:${sport}"

  local base="127.0.0.1:${port}/locklens_it?sslmode=disable"
  local log="$ARTIFACTS/integration-pg${version}.log"
  echo "    running integration tests (log: ${log#"$ROOT"/})"
  local status=0
  env -u LOCKLENS_DSN \
    GOWORK="$workdir/go.work" \
    LOCKLENS_IT_PG_VERSION="$version" \
    LOCKLENS_IT_SERVER_VERSION="$server_version" \
    LOCKLENS_IT_ADMIN_DSN="postgres://locklens_it_admin:${admin_pw}@${base}" \
    LOCKLENS_IT_DEMO_DSN="postgres://demo_app:${demo_pw}@${base}" \
    LOCKLENS_IT_OBSERVER_DSN="postgres://locklens_observer:${observer_pw}@${base}" \
    LOCKLENS_IT_LIMITED_DSN="postgres://locklens_limited:${limited_pw}@${base}" \
    LOCKLENS_IT_PGSS_OBSERVER_DSN="postgres://locklens_observer:${observer_pw}@127.0.0.1:${port}/locklens_pgss?sslmode=disable" \
    LOCKLENS_IT_PGSS_DEMO_DSN="postgres://demo_app:${demo_pw}@127.0.0.1:${port}/locklens_pgss?sslmode=disable" \
    LOCKLENS_IT_STANDBY_OBSERVER_DSN="postgres://locklens_observer:${observer_pw}@127.0.0.1:${sport}/locklens_it?sslmode=disable" \
    LOCKLENS_IT_COMPOSE_PROJECT="$project" \
    LOCKLENS_IT_COMPOSE_FILE="$COMPOSE_FILE" \
    LOCKLENS_IT_COMPOSE_ENV_FILE="$envfile" \
    LOCKLENS_IT_ARTIFACTS="$ARTIFACTS" \
    go test -tags integration -count=1 -p 1 -race -v -timeout "$GO_TEST_TIMEOUT" -run "$RUN_FILTER" $PACKAGES \
    >"$log" 2>&1 || status=$?
  grep -E '^(--- (PASS|FAIL|SKIP)|ok|FAIL|PASS)' "$log" | sed 's/^/    /' || true
  if [[ $status -ne 0 ]]; then
    echo "    FAILED (exit $status); see ${log#"$ROOT"/}"
  fi
  return $status
}

overall=0
for v in $VERSIONS; do
  run_version "$v" || overall=1
done
exit $overall
