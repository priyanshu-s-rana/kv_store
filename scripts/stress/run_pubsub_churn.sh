#!/usr/bin/env bash
# Pub/sub churn: subscribe/publish/unsubscribe loops across many clients,
# then assert (via the metrics endpoint) that active topics/subscribers
# drain back to zero — a leaked subscription or fan-in goroutine would show
# up as a gauge that never returns to 0.
#
# Usage: scripts/stress/run_pubsub_churn.sh [duration_seconds] [workers]

cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source=common.sh
source ./common.sh
trap cleanup EXIT

DURATION_SECS="${1:-15}"
WORKERS="${2:-20}"

build_binaries
start_server always

log "running pub/sub churn for ${DURATION_SECS}s with $WORKERS workers..."
"$STRESSCLIENT_BIN" -mode=pubsubchurn -addr="127.0.0.1:$SERVER_PORT" -metrics-addr="127.0.0.1:$METRICS_PORT" -workers="$WORKERS" -duration="${DURATION_SECS}s"
