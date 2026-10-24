#!/bin/sh
# Allow the lab standby to stream WAL from the lab primary (Docker network only).
set -eu
echo "host replication replicator all scram-sha-256" >> "$PGDATA/pg_hba.conf"
