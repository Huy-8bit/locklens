#!/bin/sh
# Light, steady pgbench traffic on every target instance of the scaling
# benchmark, so sessions, statements, and counters change between
# collections. BENCH_TPS transactions per second per instance.
set -eu
hosts=$(seq 1 "${BENCH_HOSTS:-10}")
for i in $hosts; do
  h="locklens-bench-target-$i"
  until pg_isready -q -h "$h" -U postgres; do sleep 1; done
  pgbench -i -q -s 1 -h "$h" -U postgres postgres >/dev/null 2>&1 || true
done
for i in $hosts; do
  h="locklens-bench-target-$i"
  (
    while true; do
      pgbench -n -c 3 -j 1 -R "${BENCH_TPS:-15}" -T 600 -h "$h" -U postgres postgres >/dev/null 2>&1 || sleep 2
    done
  ) &
done
wait
