#!/usr/bin/env bash
# Lab scenario "outage": stop the lab primary, keep it down for a while, then
# start it again and wait for its health check. Only touches the lab Compose
# project (name: locklens). Usage: scripts/lab-outage.sh [down-seconds]
set -euo pipefail
cd "$(dirname "$0")/.."
DOWN=${1:-60}
compose=(docker compose -p locklens -f deploy/compose.yaml)
echo "$(date +%T) stopping the lab primary for ${DOWN}s"
"${compose[@]}" stop primary
# The scenario's purpose is a period without the database; its length is the
# only timed part. Recovery below is detected by the health check.
sleep "$DOWN"
echo "$(date +%T) starting the lab primary"
"${compose[@]}" up -d --wait --wait-timeout 120 primary
echo "$(date +%T) primary healthy again; LockLens reconnects on its next attempt"
