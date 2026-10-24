#!/usr/bin/env bash
# Run the API locally with the demo databases and Vite's development proxy.
set -euo pipefail
cd "$(dirname "$0")/.."
docker compose -f deploy/compose.yaml up -d --wait --wait-timeout 180 store primary standby

# Fake local lab credentials; environment overrides support other databases.
export LOCKLENS_STORE_DSN=${LOCKLENS_STORE_DSN:-"postgres://locklens_store:lab-store-pw@127.0.0.1:${LOCKLENS_STORE_PORT:-55434}/locklens?sslmode=disable"}
export LOCKLENS_MASTER_KEY=${LOCKLENS_MASTER_KEY:-bG9ja2xlbnMtbGFiLW9ubHktbm90LWEtcmVhbC1rZXk=}
export LOCKLENS_ADMIN_PASSWORD=${LOCKLENS_ADMIN_PASSWORD:-locklens-lab-admin}
export LOCKLENS_COOKIE_SECURE=${LOCKLENS_COOKIE_SECURE:-false}
export LOCKLENS_DEMO_MODE=${LOCKLENS_DEMO_MODE:-true}
export LOCKLENS_LISTEN=${LOCKLENS_LISTEN:-127.0.0.1:18080}
export LOCKLENS_DEV_API=${LOCKLENS_DEV_API:-http://$LOCKLENS_LISTEN}
export LOCKLENS_BOOTSTRAP_TARGETS_FILE=${LOCKLENS_BOOTSTRAP_TARGETS_FILE:-"$PWD/demo/targets.json"}
export LOCKLENS_LAB_PRIMARY_DSN=${LOCKLENS_LAB_PRIMARY_DSN:-"postgres://locklens_observer:observer-pw@127.0.0.1:${LOCKLENS_PG_PORT:-55432}/locklens_demo?sslmode=disable"}
export LOCKLENS_LAB_STANDBY_DSN=${LOCKLENS_LAB_STANDBY_DSN:-"postgres://locklens_observer:observer-pw@127.0.0.1:${LOCKLENS_STANDBY_PORT:-55433}/locklens_demo?sslmode=disable"}

bin/locklens server &
server_pid=$!
cleanup() {
  kill "$server_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT TERM
for attempt in $(seq 1 60); do
  kill -0 "$server_pid" 2>/dev/null || { wait "$server_pid"; exit 1; }
  if curl -fsS "$LOCKLENS_DEV_API/readyz" >/dev/null 2>&1; then
    cd web
    npm run dev -- --host 127.0.0.1 --port "${LOCKLENS_DEV_WEB_PORT:-5173}" --strictPort
    exit $?
  fi
  sleep 1
done
echo "The development API did not become ready at $LOCKLENS_DEV_API/readyz" >&2
exit 1
