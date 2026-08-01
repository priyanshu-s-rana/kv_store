#!/usr/bin/env bash
# Random read/write workload with periodic invariant verification, while
# checkpoints fire concurrently against the same sustained write load.
#
# Usage: scripts/stress/run_readwrite_checkpoint.sh [duration_seconds] [workers]

cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source=common.sh
source ./common.sh
trap cleanup EXIT

DURATION_SECS="${1:-20}"
WORKERS="${2:-20}"

build_binaries
start_server always

# Fire a CHECKPOINT roughly every second for the duration of the workload,
# racing it against the concurrent write burst below.
(
	end=$((SECONDS + DURATION_SECS))
	while [ $SECONDS -lt "$end" ]; do
		resp_cmd CHECKPOINT >/dev/null 2>&1 || true
		sleep 1
	done
) &
CHECKPOINT_LOOP_PID=$!

log "running readwrite workload for ${DURATION_SECS}s with $WORKERS workers, checkpointing concurrently..."
set +e
"$STRESSCLIENT_BIN" -mode=readwrite -addr="127.0.0.1:$SERVER_PORT" -workers="$WORKERS" -duration="${DURATION_SECS}s"
STATUS=$?
set -e

kill "$CHECKPOINT_LOOP_PID" 2>/dev/null || true
wait "$CHECKPOINT_LOOP_PID" 2>/dev/null || true

exit $STATUS
