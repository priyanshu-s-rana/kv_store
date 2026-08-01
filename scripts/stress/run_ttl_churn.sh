#!/usr/bin/env bash
# Continuous TTL churn: set-with-TTL in a loop, verifying expiry happens
# within a bounded window (neither too early nor unboundedly late).
#
# Usage: scripts/stress/run_ttl_churn.sh [duration_seconds] [workers]

cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source=common.sh
source ./common.sh
trap cleanup EXIT

DURATION_SECS="${1:-15}"
WORKERS="${2:-8}"

build_binaries
start_server always

log "running TTL churn for ${DURATION_SECS}s with $WORKERS workers..."
"$STRESSCLIENT_BIN" -mode=ttlchurn -addr="127.0.0.1:$SERVER_PORT" -workers="$WORKERS" -duration="${DURATION_SECS}s"
