#!/usr/bin/env bash
# Scaling benchmark of LockLens: N targets on M servers sharing one store.
#
#   scripts/bench/scale.sh run 300 3 local     # one run
#   scripts/bench/scale.sh matrix              # N in 20 100 300, M in 1 2 3, profiles local and rtt
#
# Profiles: local (servers reach the store directly) and rtt (through
# toxiproxy, +1 ms each way, about +2 ms round trip, as with a store on
# another host). Every run starts from empty volumes, creates the targets
# through the API, waits BENCH_WARMUP, then measures for BENCH_DURATION.
# Results: test-results/bench/<label>.json (see tests/e2e/cmd/scalebench).
#
# Knobs (environment): BENCH_HOSTS (target instances, 10), BENCH_TPS
# (pgbench rate per instance, 15), BENCH_WARMUP (3m), BENCH_DURATION (10m),
# BENCH_CONCURRENCY (LOCKLENS_MAX_CONCURRENT_COLLECTIONS, 4),
# BENCH_STORE_CPUS (2), BENCH_NODE_CPUS (2), BENCH_TARGET_CPUS (0.5).
#
# The numbers describe this machine and this harness, not capacity
# guarantees: target instances carry many targets each, so collector
# durations include contention on the monitored side.
set -euo pipefail
cd "$(dirname "$0")/../.."
ROOT=$(pwd)

export BENCH_HOSTS=${BENCH_HOSTS:-10}
WARMUP=${BENCH_WARMUP:-3m}
DURATION=${BENCH_DURATION:-10m}
OUT=${BENCH_OUT:-$ROOT/test-results/bench}
TOOL="$OUT/scalebench"
STORE_HOST_DSN="postgres://locklens_store:bench-store-pw@127.0.0.1:${BENCH_STORE_PORT:-55444}/locklens?sslmode=disable"
TOXI="http://127.0.0.1:${BENCH_TOXI_PORT:-58474}"
compose=(docker compose -f deploy/compose.bench.yaml)

mkdir -p "$OUT"

build() {
  echo "== building locklens-backend:bench and the measurement tool"
  docker build -q -t locklens-backend:bench --build-arg VERSION="bench-$(git rev-parse --short HEAD)" backend >/dev/null
  go -C tests/e2e build -o "$TOOL" ./cmd/scalebench
}

down() {
  "${compose[@]}" --profile nodes --profile load down -v --remove-orphans >/dev/null 2>&1 || true
}

wait_ready() {
  local url=$1
  for _ in $(seq 1 120); do
    if curl -fsS -o /dev/null "$url/readyz"; then return 0; fi
    sleep 1
  done
  echo "server $url did not become ready" >&2
  return 1
}

run() {
  local n=$1 nodes=$2 profile=$3
  local label="n${n}-nodes${nodes}-${profile}"
  echo "== $label (warm-up $WARMUP, window $DURATION)"
  down
  "${compose[@]}" up -d --wait --scale target="$BENCH_HOSTS" store toxiproxy target >/dev/null

  export BENCH_STORE_ADDR=store:5432
  if [[ $profile == rtt ]]; then
    curl -fsS -X POST "$TOXI/proxies" -d '{"name":"store","listen":"0.0.0.0:15432","upstream":"store:5432","enabled":true}' >/dev/null
    for stream in upstream downstream; do
      curl -fsS -X POST "$TOXI/proxies/store/toxics" \
        -d "{\"name\":\"lat_$stream\",\"type\":\"latency\",\"stream\":\"$stream\",\"attributes\":{\"latency\":1,\"jitter\":0}}" >/dev/null
    done
    export BENCH_STORE_ADDR=toxiproxy:15432
  fi

  "${compose[@]}" --profile load up -d load >/dev/null
  # One server first (migrations, administrator), then the others.
  "${compose[@]}" --profile nodes up -d --wait --scale locklens=1 locklens >/dev/null
  if (( nodes > 1 )); then
    "${compose[@]}" --profile nodes up -d --wait --scale locklens="$nodes" --no-recreate locklens >/dev/null
  fi
  local urls=() containers="store=locklens-bench-store-1,target1=locklens-bench-target-1"
  for i in $(seq 1 "$nodes"); do
    local addr
    addr=$("${compose[@]}" --profile nodes port --index "$i" locklens 8080)
    urls+=("http://$addr")
    wait_ready "http://$addr"
    containers+=",node$i=locklens-bench-locklens-$i"
  done
  "$TOOL" setup -url "${urls[0]}" -targets "$n" -hosts "$BENCH_HOSTS"
  "$TOOL" measure -nodes "$(IFS=,; echo "${urls[*]}")" -store "$STORE_HOST_DSN" -containers "$containers" \
    -warmup "$WARMUP" -duration "$DURATION" -label "$label" -out "$OUT/$label.json" >/dev/null
  echo "   → $OUT/$label.json"
  # Server logs of the run, for warnings (lease failures, slow collectors).
  "${compose[@]}" --profile nodes logs --no-color locklens > "$OUT/$label.log" 2>&1 || true
  down
}

environment() {
  {
    echo "date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "commit: $(git rev-parse --short HEAD)"
    echo "host: $(sysctl -n machdep.cpu.brand_string 2>/dev/null || uname -m), $(sysctl -n hw.ncpu 2>/dev/null || nproc) CPUs, $(( $(sysctl -n hw.memsize 2>/dev/null || echo 0) / 1073741824 )) GiB"
    docker info --format 'docker: {{.ServerVersion}}, VM {{.NCPU}} CPUs, {{.MemTotal}} bytes'
    echo "other containers: $(docker ps --format '{{.Names}}' | grep -v '^locklens-bench' | tr '\n' ' ')"
    echo "hosts=$BENCH_HOSTS tps=${BENCH_TPS:-15} concurrency=${BENCH_CONCURRENCY:-4} store_cpus=${BENCH_STORE_CPUS:-2} node_cpus=${BENCH_NODE_CPUS:-2} target_cpus=${BENCH_TARGET_CPUS:-0.5}"
    echo "warmup=$WARMUP duration=$DURATION"
  } > "$OUT/environment.txt"
}

case "${1:-}" in
  run)
    build
    environment
    trap down EXIT
    run "${2:?targets}" "${3:?nodes}" "${4:-local}"
    ;;
  matrix)
    build
    environment
    trap down EXIT
    for profile in ${BENCH_PROFILES:-local rtt}; do
      for n in ${BENCH_TARGETS:-20 100 300}; do
        for nodes in ${BENCH_NODES:-1 2 3}; do
          run "$n" "$nodes" "$profile"
        done
      done
    done
    ;;
  *)
    echo "usage: $0 run N NODES [local|rtt] | matrix" >&2
    exit 2
    ;;
esac
