#!/usr/bin/env bash
# Repeated crash/restart cycles against the same data directory: write,
# SIGKILL, restart, verify every previously-acknowledged write is still
# present, then write more and repeat. Exits non-zero with the exact
# mismatch on the first violation.
#
# Portability note: deliberately avoids `declare -A` (associative arrays) —
# stock macOS ships bash 3.2, which doesn't support them. Two parallel
# indexed arrays stand in for a key->value map instead.
#
# Usage: scripts/stress/run_crash_cycles.sh [iterations]

cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source=common.sh
source ./common.sh
trap cleanup EXIT

ITERATIONS="${1:-10}"

build_binaries

KEYS=()
VALS=()
FAIL=0

for ((i = 0; i < ITERATIONS; i++)); do
	start_server always

	# Verify every key written in a previous iteration survived the crash.
	for idx in "${!KEYS[@]}"; do
		key="${KEYS[$idx]}"
		want="${VALS[$idx]}"
		got_val=$(get_value "$key")
		if [ "$got_val" != "$want" ]; then
			echo "INVARIANT VIOLATION: iteration $i: key $key = '$got_val', want '$want' (lost across a crash/restart cycle)" >&2
			FAIL=1
			break 2
		fi
	done

	key="cycle-$i"
	val="val-$i-$RANDOM"
	resp_cmd SET "$key" "$val" >/dev/null
	KEYS+=("$key")
	VALS+=("$val")

	kill_server_hard
done

if [ "$FAIL" -eq 0 ]; then
	log "completed $ITERATIONS crash/restart cycles, all ${#KEYS[@]} accumulated keys verified intact each time"
fi

exit $FAIL
