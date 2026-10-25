#!/bin/sh
# Entry point of the lab standby: on first start, clone the primary with
# pg_basebackup (creating a dedicated physical replication slot), then start
# PostgreSQL in hot-standby mode. Readiness of the primary is polled with a
# bounded timeout; there is no fixed sleep.
set -eu

if [ ! -s "$PGDATA/PG_VERSION" ]; then
  echo "standby: waiting for the primary"
  i=0
  PRIMARY_HOST=${PRIMARY_HOST:-primary}
  until pg_isready -h "$PRIMARY_HOST" -p 5432 -U replicator -q; do
    i=$((i + 1))
    if [ "$i" -gt 120 ]; then
      echo "standby: primary not ready after 120 attempts" >&2
      exit 1
    fi
    sleep 1
  done
  echo "standby: cloning the primary"
  # The container runs as the postgres user (see docker-compose.yml).
  REPLICATION_SLOT=${REPLICATION_SLOT:-standby1_slot}
  PGPASSWORD="$REPLICATION_PASSWORD" pg_basebackup -h "$PRIMARY_HOST" -p 5432 -U replicator -D "$PGDATA" -R -X stream -C -S "$REPLICATION_SLOT" -c fast
fi

exec docker-entrypoint.sh "$@"
