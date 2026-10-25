#!/usr/bin/env bash
# Verify the backend and frontend images together on disposable databases:
# readiness, migrations, the UI and its assets, SPA routing, security headers,
# the frontend's /api/ proxy (session cookie and CSRF origin check), the CLI,
# the lab runner, and non-root users.
set -euo pipefail
cd "$(dirname "$0")/.."
project="locklens-smoke-$(date +%s)-$$"
free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}
export LOCKLENS_HTTP_PORT=$(free_port) LOCKLENS_STORE_PORT=$(free_port) LOCKLENS_PG_PORT=$(free_port) LOCKLENS_STANDBY_PORT=$(free_port) \
  LOCKLENS_STANDBY2_PORT=$(free_port) LOCKLENS_BACKEND_PORT=$(free_port)
compose=(docker compose -p "$project" -f deploy/compose.yaml -f deploy/compose.lab.yaml --env-file deploy/lab.env)
work=$(mktemp -d)
cleanup() {
  local status=$?
  if [[ $status -ne 0 ]]; then
    "${compose[@]}" logs --no-color --tail 80 backend frontend >&2 || true
  fi
  "${compose[@]}" --profile lab down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM
"${compose[@]}" up -d --build --wait --wait-timeout 300
base="http://127.0.0.1:$LOCKLENS_HTTP_PORT"

# Backend: health, readiness, and no UI of its own.
"${compose[@]}" exec -T backend wget -q -O - http://127.0.0.1:8080/healthz | grep -q '"ok"'
"${compose[@]}" exec -T backend wget -q -O - http://127.0.0.1:8080/readyz | grep -q '"ready"'
"${compose[@]}" exec -T backend sh -c 'wget -q -O - http://127.0.0.1:8080/ 2>&1 || true' | grep -q -v '<div id="root">'

# Frontend: UI, assets, SPA routing, headers.
curl -fsS "$base/frontend-healthz" >/dev/null
curl -fsS -D "$work/headers.txt" "$base/" >"$work/index.html"
curl -fsS "$base/targets/1/blocking" >"$work/route.html"
asset=$(python3 - "$work/index.html" "$work/route.html" "$work/headers.txt" <<'PY'
import re, sys
html, route, headers = (open(p).read() for p in sys.argv[1:])
h = headers.lower()
assert '<div id="root">' in html, 'React entrypoint missing'
assert html == route, 'SPA route serves a different document'
for want in ("content-security-policy: default-src 'self'", 'x-frame-options: deny', 'x-content-type-options: nosniff', 'cache-control: no-store'):
    assert want in h, f'missing header: {want}'
assert 'server: nginx/' not in h, 'nginx version must not be advertised'
match = re.search(r'src="(/assets/[^\"]+\.js)"', html)
assert match, 'JavaScript asset missing'
print(match[1])
PY
)
curl -fsS -D "$work/asset-headers.txt" "$base$asset" -o "$work/app.js"
test -s "$work/app.js"
grep -qi 'cache-control: public, max-age=31536000, immutable' "$work/asset-headers.txt"
test "$(curl -s -o /dev/null -w '%{http_code}' "$base/assets/missing.js")" = 404

# The API through the frontend's proxy: same origin, cookie, CSRF origin check.
test "$(curl -s -o /dev/null -w '%{http_code}' "$base/api/v1/session")" = 401
curl -fsS -c "$work/jar" -D "$work/login-headers.txt" -H 'Content-Type: application/json' -H "Origin: $base" \
  -d '{"username":"admin","password":"locklens-lab-admin"}' "$base/api/v1/auth/login" >"$work/login.json"
grep -qi 'set-cookie: locklens_session=.*httponly' "$work/login-headers.txt"
grep -qi 'samesite=strict' "$work/login-headers.txt"
csrf=$(python3 -c 'import json,sys; s=json.load(open(sys.argv[1])); assert s["role"] == "admin"; print(s["csrf_token"])' "$work/login.json")
curl -fsS -b "$work/jar" "$base/api/v1/fleet" | python3 -c 'import json,sys; assert len(json.load(sys.stdin)["targets"]) >= 1'
test "$(curl -s -o /dev/null -w '%{http_code}' -b "$work/jar" -H "X-CSRF-Token: $csrf" -H 'Origin: http://evil.example' -X POST "$base/api/v1/auth/logout")" = 403

# CLI, lab runner, users.
"${compose[@]}" exec -T backend locklens version
"${compose[@]}" run --rm --no-deps lab list >/dev/null
test "$("${compose[@]}" exec -T backend id -u)" != 0
test "$("${compose[@]}" exec -T frontend id -u)" != 0
echo "Docker smoke passed: backend readiness and migrations, frontend UI/assets/SPA routing/headers, API proxy with session cookie and CSRF origin check, CLI, lab runner, non-root users."
