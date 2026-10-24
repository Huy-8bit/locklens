#!/usr/bin/env bash
# Verify a production image and its embedded assets on disposable databases.
set -euo pipefail
cd "$(dirname "$0")/.."
project="locklens-smoke-$(date +%s)-$$"
free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}
export LOCKLENS_HTTP_PORT=$(free_port) LOCKLENS_STORE_PORT=$(free_port) LOCKLENS_PG_PORT=$(free_port) LOCKLENS_STANDBY_PORT=$(free_port)
compose=(docker compose -p "$project" -f deploy/compose.yaml)
work=$(mktemp -d)
cleanup() {
  local status=$?
  if [[ $status -ne 0 ]]; then
    "${compose[@]}" logs --no-color --tail 80 locklens >&2 || true
  fi
  "${compose[@]}" --profile lab down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM
"${compose[@]}" up -d --build --wait --wait-timeout 300
base="http://127.0.0.1:$LOCKLENS_HTTP_PORT"
curl -fsS "$base/healthz" >/dev/null
curl -fsS "$base/readyz" >/dev/null
curl -fsS "$base/" >"$work/index.html"
asset=$(python3 - "$work/index.html" <<'PY'
import re, sys
html = open(sys.argv[1]).read()
assert '<div id="root">' in html, 'React entrypoint missing from embedded UI'
match = re.search(r'src="(/assets/[^\"]+\.js)"', html)
assert match, 'Embedded JavaScript asset missing'
print(match[1])
PY
)
curl -fsS "$base$asset" >"$work/app.js"
test -s "$work/app.js"
curl -fsS "$base/targets/1/blocking" >"$work/route.html"
cmp "$work/index.html" "$work/route.html"
"${compose[@]}" exec -T locklens locklens version
"${compose[@]}" run --rm --no-deps lab list
user=$("${compose[@]}" exec -T locklens id -u)
test "$user" != 0
echo "Docker smoke passed: readiness, migrations, embedded UI/assets, SPA routing, CLI, lab runner, non-root user."
