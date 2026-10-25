#!/usr/bin/env bash
# Performance smoke check against the Docker lab (make up or make db-up).
#
# Starts a separate `locklens server` with its own, temporary store database
# and PERF_TARGETS targets (half on the lab primary, half on the standby),
# lets it collect for PERF_DURATION seconds with production intervals, and
# reports:
#   - collection duration per collector (mean and approximate p95),
#   - concurrent collector runs (must stay <= LOCKLENS_MAX_CONCURRENT_COLLECTIONS),
#   - connections the server holds to each database,
#   - resident memory and CPU time of the server process,
#   - monitoring store growth and write-queue drops.
#
# The numbers describe this machine and this lab only; they are not a
# capacity guarantee. Results are written to test-results/perf-smoke.txt.
#
#   PERF_TARGETS=20 PERF_DURATION=300 PERF_INTERVAL=10 ./scripts/perf-smoke.sh
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$(pwd)

TARGETS=${PERF_TARGETS:-20}
DURATION=${PERF_DURATION:-300}
INTERVAL=${PERF_INTERVAL:-10}
CONCURRENCY=${LOCKLENS_MAX_CONCURRENT_COLLECTIONS:-4}
STORE_PORT=${LOCKLENS_STORE_PORT:-55434}
PG_PORT=${LOCKLENS_PG_PORT:-55432}
STANDBY_PORT=${LOCKLENS_STANDBY_PORT:-55433}
compose=(docker compose -f deploy/compose.yaml -f deploy/compose.lab.yaml --env-file deploy/lab.env)
db="locklens_perf_$(date +%s)"
work=$(mktemp -d)
out="$ROOT/test-results/perf-smoke.txt"
mkdir -p "$ROOT/test-results"

for svc in store primary standby; do
  if [[ -z "$("${compose[@]}" ps -q --status running "$svc" 2>/dev/null)" ]]; then
    echo "the lab service '$svc' is not running; start the lab with: make up" >&2
    exit 2
  fi
done
[[ -x bin/locklens ]] || make build >/dev/null

pid=""
cleanup() {
  [[ -n "$pid" ]] && kill "$pid" 2>/dev/null && wait "$pid" 2>/dev/null || true
  "${compose[@]}" exec -T store psql -q -U locklens_store -d locklens -c "DROP DATABASE IF EXISTS $db WITH (FORCE)" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

"${compose[@]}" exec -T store psql -q -U locklens_store -d locklens -c "CREATE DATABASE $db" >/dev/null

# Bootstrap file: DSNs come from environment variables, never from the file.
{
  echo "["
  for i in $(seq 1 "$TARGETS"); do
    env=PERF_PRIMARY_DSN
    (( i % 2 == 0 )) && env=PERF_STANDBY_DSN
    sep=","; (( i == TARGETS )) && sep=""
    printf '  {"name": "perf-%02d", "dsn_env": "%s", "interval_seconds": %d, "max_connections": 1}%s\n' "$i" "$env" "$INTERVAL" "$sep"
  done
  echo "]"
} >"$work/targets.json"

port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')
# Fake, lab-only values.
LOCKLENS_LISTEN="127.0.0.1:$port" \
LOCKLENS_STORE_DSN="postgres://locklens_store:lab-store-pw@127.0.0.1:$STORE_PORT/$db?sslmode=disable" \
LOCKLENS_MASTER_KEY="bG9ja2xlbnMtbGFiLW9ubHktbm90LWEtcmVhbC1rZXk=" \
LOCKLENS_ADMIN_PASSWORD="perf-smoke-$(date +%s)" \
LOCKLENS_BOOTSTRAP_TARGETS_FILE="$work/targets.json" \
LOCKLENS_MAX_CONCURRENT_COLLECTIONS="$CONCURRENCY" \
LOCKLENS_LOG_LEVEL=warn \
PERF_PRIMARY_DSN="postgres://locklens_observer:observer-pw@127.0.0.1:$PG_PORT/locklens_demo?sslmode=disable" \
PERF_STANDBY_DSN="postgres://locklens_observer:observer-pw@127.0.0.1:$STANDBY_PORT/locklens_demo?sslmode=disable" \
  bin/locklens server >"$work/server.log" 2>&1 &
pid=$!

for _ in $(seq 1 60); do
  curl -fs "http://127.0.0.1:$port/readyz" >/dev/null 2>&1 && break
  sleep 0.5
done
curl -fs "http://127.0.0.1:$port/readyz" >/dev/null || { cat "$work/server.log" >&2; exit 1; }

store_size() {
  "${compose[@]}" exec -T store psql -At -U locklens_store -d locklens -c "SELECT pg_database_size('$db')"
}
conns() { # established connections of the server process to a port
  lsof -nP -a -p "$pid" -iTCP:"$1" -sTCP:ESTABLISHED 2>/dev/null | tail -n +2 | wc -l | tr -d ' '
}

size0=$(store_size)
echo "perf smoke: $TARGETS targets, interval ${INTERVAL}s, concurrency limit $CONCURRENCY, ${DURATION}s"
: >"$work/samples"
end=$(( $(date +%s) + DURATION ))
while (( $(date +%s) < end )); do
  m=$(curl -fs "http://127.0.0.1:$port/metrics")
  busy=$(awk '/^locklens_collect_slots_busy /{print $2}' <<<"$m")
  queue=$(awk '/^locklens_write_queue_length /{print $2}' <<<"$m")
  rss=$(ps -o rss= -p "$pid" | tr -d ' ')
  echo "$busy $queue $rss $(conns "$PG_PORT") $(conns "$STANDBY_PORT") $(conns "$STORE_PORT")" >>"$work/samples"
  sleep 2
done
size1=$(store_size)
curl -fs "http://127.0.0.1:$port/metrics" >"$work/metrics.txt"
cpu=$(ps -o time= -p "$pid" | tr -d ' ')
ready=$(curl -fs "http://127.0.0.1:$port/api/v1/session" -o /dev/null -w '%{http_code}' || true)

python3 -I - "$work/metrics.txt" "$work/samples" "$TARGETS" "$INTERVAL" "$CONCURRENCY" "$DURATION" "$size0" "$size1" "$cpu" >"$out" <<'EOF'
import re, sys
metrics, samples, targets, interval, conc, dur, s0, s1, cpu = sys.argv[1:]
text = open(metrics).read()
def val(name):
    m = re.search(r'^' + name + r' ([0-9.e+]+)$', text, re.M)
    return float(m.group(1)) if m else 0.0
print(f"LockLens perf smoke: {targets} targets (half primary, half standby), interval {interval}s, "
      f"concurrency limit {conc}, {dur}s, production intervals")
print()
print(f"{'collector':<12} {'runs':>6} {'mean ms':>8} {'p95 ms (bucket)':>16} {'errors':>7}")
for c in ["activity", "database", "statements", "replication", "tables", "host"]:
    cnt = val(f'locklens_collect_duration_seconds_count{{collector="{c}"}}')
    if cnt == 0:
        continue
    total = val(f'locklens_collect_duration_seconds_sum{{collector="{c}"}}')
    buckets = sorted((float(le), float(v)) for le, v in re.findall(
        r'^locklens_collect_duration_seconds_bucket\{collector="' + c + r'",le="([0-9.e+]+)"\} ([0-9.]+)$', text, re.M))
    p95 = next((le for le, v in buckets if v >= 0.95 * cnt), float("inf"))
    errs = val(f'locklens_collect_total{{collector="{c}",outcome="error"}}')
    print(f"{c:<12} {int(cnt):>6} {1000*total/cnt:>8.1f} {('<= %.0f' % (p95*1000)) if p95 != float('inf') else '> max':>16} {int(errs):>7}")
rows = [list(map(float, l.split())) for l in open(samples) if l.strip()]
col = lambda i: [r[i] for r in rows]
print()
print(f"concurrent collector runs: max {int(max(col(0)))} (limit {conc})")
print(f"write queue length: max {int(max(col(1)))}; dropped batches: {int(val('locklens_store_dropped_batches_total'))}; "
      f"store write errors: {int(val('locklens_store_write_errors_total'))}")
print(f"resident memory: max {max(col(2))/1024:.1f} MiB, last {col(2)[-1]/1024:.1f} MiB")
print(f"CPU time of the server process: {cpu}")
print(f"connections held: primary max {int(max(col(3)))}, standby max {int(max(col(4)))}, store max {int(max(col(5)))}")
growth = (int(s1) - int(s0)) / 1024 / 1024
print(f"store growth: {growth:.2f} MiB in {dur}s ({growth*3600/float(dur)/int(targets):.2f} MiB per target-hour)")
EOF
cat "$out"
echo "(written to ${out#"$ROOT"/})"
