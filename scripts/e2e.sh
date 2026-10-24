#!/usr/bin/env bash
# Browser end-to-end tests against an isolated, real LockLens stack.
#
#   1. starts deploy/compose.yaml under a unique project name on random
#      loopback ports (LockLens, store, primary, standby);
#   2. runs the Playwright suite in tests/e2e/browser against it (no API mocks);
#   3. removes only this project's containers and volumes.
#
# Requirements: Docker with Compose v2, Node.js, and the Playwright Chromium
# browser (cd tests/e2e && npm ci && npx playwright install chromium).
# Set LOCKLENS_E2E_KEEP=1 to keep the stack for debugging.
# Reports go to test-results/e2e-report, traces to test-results/e2e-artifacts.
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT=$(pwd)
COMPOSE_FILE="$ROOT/deploy/compose.yaml"
project="locklens-e2e-$(date +%s)-$$"

free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}

export LOCKLENS_HTTP_PORT=$(free_port) LOCKLENS_STORE_PORT=$(free_port) LOCKLENS_PG_PORT=$(free_port) LOCKLENS_STANDBY_PORT=$(free_port)
dc() { docker compose -p "$project" -f "$COMPOSE_FILE" "$@"; }

cleanup() {
  local status=$?
  if [[ $status -ne 0 ]]; then
    mkdir -p test-results
    dc logs --no-color locklens >test-results/e2e-locklens.log 2>&1 || true
    echo "LockLens server log: test-results/e2e-locklens.log"
  fi
  if [[ "${LOCKLENS_E2E_KEEP:-0}" == "1" ]]; then
    echo "Kept stack $project at http://127.0.0.1:$LOCKLENS_HTTP_PORT; remove it with:"
    echo "  docker compose -p $project -f $COMPOSE_FILE --profile lab down -v --remove-orphans"
  else
    dc --profile lab down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  exit $status
}
trap cleanup EXIT

echo "==> starting stack $project (http://127.0.0.1:$LOCKLENS_HTTP_PORT)"
dc up -d --build --wait --wait-timeout 300

echo "==> running Playwright"
cd tests/e2e
npm ci --no-audit --no-fund
LOCKLENS_E2E_BASE_URL="http://127.0.0.1:$LOCKLENS_HTTP_PORT" \
LOCKLENS_E2E_COMPOSE_PROJECT="$project" \
LOCKLENS_E2E_COMPOSE_FILE="$COMPOSE_FILE" \
LOCKLENS_E2E_ADMIN_PASSWORD="locklens-lab-admin" \
  npx playwright test "$@"
