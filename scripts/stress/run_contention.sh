#!/usr/bin/env bash
# Many clients competing for the same shared counter, asserting exact
# mutual exclusion (no lost updates) via the single-threaded event loop's
# serialization of INCR.
#
# Note: there is no server-enforced lock/acquire-release primitive in this
# codebase (see the full test suite report — the only related thing is a
# "lock-released:<key>" pub/sub notification convention published on DEL
# and TTL eviction, not a real lock). This script exercises the closest
# real analogue to lock contention that actually exists: concurrent
# mutation of shared state serialized by the event loop.
#
# Usage: scripts/stress/run_contention.sh [duration_seconds] [workers]

cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source=common.sh
source ./common.sh
trap cleanup EXIT

DURATION_SECS="${1:-15}"
WORKERS="${2:-30}"

build_binaries
start_server always

log "running contention workload for ${DURATION_SECS}s with $WORKERS workers..."
"$STRESSCLIENT_BIN" -mode=contention -addr="127.0.0.1:$SERVER_PORT" -workers="$WORKERS" -duration="${DURATION_SECS}s"
