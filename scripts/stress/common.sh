#!/usr/bin/env bash
# Shared helpers for the scripts in scripts/stress/. Sourced, not executed
# directly. Every driver script that sources this must call `trap cleanup
# EXIT` itself, immediately after sourcing, so a failure anywhere still
# tears down the server process and temp data directory.

set -euo pipefail

STRESS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$STRESS_DIR/../.." && pwd)"

BIN_DIR="$(mktemp -d /tmp/kv-stress-bin.XXXXXX)"
DATA_DIR="$(mktemp -d /tmp/kv-stress-data.XXXXXX)"
SERVER_BIN="$BIN_DIR/kv-server"
STRESSCLIENT_BIN="$BIN_DIR/stressclient"

SERVER_PID=""
SERVER_PORT=""
METRICS_PORT=""

log() { echo "[$(date +%H:%M:%S)] $*"; }

build_binaries() {
	log "building kv-server and stressclient..."
	(cd "$REPO_ROOT" && go build -o "$SERVER_BIN" ./cmd/kv-server)
	(cd "$REPO_ROOT" && go build -o "$STRESSCLIENT_BIN" ./scripts/stress/stressclient)
}

# free_port prints an available TCP port on 127.0.0.1. Prefers asking the OS
# for a real ephemeral port (via python3); falls back to a random high port
# if python3 isn't available — rare collisions there just fail the script
# with a clear "server did not come up" error, safe to just rerun.
free_port() {
	if command -v python3 >/dev/null 2>&1; then
		python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'
	else
		echo $((20000 + RANDOM % 40000))
	fi
}

# start_server launches kv-server against DATA_DIR with the given sync
# policy, waits (bounded) for it to accept connections, and sets
# SERVER_PID/SERVER_PORT/METRICS_PORT for callers.
start_server() {
	local sync_policy="${1:-always}"
	SERVER_PORT=$(free_port)
	METRICS_PORT=$(free_port)

	(
		cd "$REPO_ROOT"
		SERVER_HOST=127.0.0.1 SERVER_PORT="$SERVER_PORT" \
			METRICS_HOST=127.0.0.1 METRICS_PORT="$METRICS_PORT" \
			PERSISTENCE_JOURNAL_PATH1="$DATA_DIR/journal_0.aof" \
			PERSISTENCE_JOURNAL_PATH2="$DATA_DIR/journal_1.aof" \
			PERSISTENCE_SNAPSHOT_PATH="$DATA_DIR/dump.gob" \
			PERSISTENCE_JOURNAL_POLICY="$sync_policy" \
			"$SERVER_BIN" >"$DATA_DIR/server.log" 2>&1 &
		echo $! >"$DATA_DIR/server.pid"
	)
	SERVER_PID=$(cat "$DATA_DIR/server.pid")

	local deadline=$((SECONDS + 10))
	while ! (exec 3<>"/dev/tcp/127.0.0.1/$SERVER_PORT") 2>/dev/null; do
		if [ $SECONDS -ge $deadline ]; then
			log "server did not come up within 10s; log:"
			cat "$DATA_DIR/server.log" >&2
			exit 1
		fi
		sleep 0.05
	done
	exec 3<&- 3>&- 2>/dev/null || true
	log "server up: pid=$SERVER_PID addr=127.0.0.1:$SERVER_PORT metrics=127.0.0.1:$METRICS_PORT"
}

stop_server_graceful() {
	[ -n "$SERVER_PID" ] || return 0
	kill -TERM "$SERVER_PID" 2>/dev/null || true
	wait "$SERVER_PID" 2>/dev/null || true
	SERVER_PID=""
}

kill_server_hard() {
	[ -n "$SERVER_PID" ] || return 0
	kill -KILL "$SERVER_PID" 2>/dev/null || true
	wait "$SERVER_PID" 2>/dev/null || true
	SERVER_PID=""
}

# resp_cmd sends a RESP array command over a fresh TCP connection and prints
# the raw reply line. Good enough for simple SET/GET-style verification
# without pulling in the SDK for one-off checks in bash.
resp_cmd() {
	local host="127.0.0.1" port="$SERVER_PORT"
	local out="*$#\r\n"
	for arg in "$@"; do
		out+="\$${#arg}\r\n${arg}\r\n"
	done
	exec 3<>"/dev/tcp/$host/$port"
	printf '%b' "$out" >&3
	local reply
	IFS= read -r -u3 reply || true
	exec 3<&- 3>&-
	printf '%s' "$reply"
}

# get_value sends GET <key> over a fresh connection and prints just the
# decoded value ("" for a nil reply), reading both the bulk-string header
# and (if present) its value line.
get_value() {
	local key="$1"
	local body="\$3\r\nGET\r\n\$${#key}\r\n${key}\r\n"
	exec 3<>"/dev/tcp/127.0.0.1/$SERVER_PORT"
	printf '*2\r\n%b' "$body" >&3
	local header
	IFS= read -r -u3 header
	header="${header%$'\r'}"
	if [ "$header" = '$-1' ]; then
		exec 3<&- 3>&-
		printf ''
		return
	fi
	local value
	IFS= read -r -u3 value
	value="${value%$'\r'}"
	exec 3<&- 3>&-
	printf '%s' "$value"
}

cleanup() {
	local status=$?
	kill_server_hard 2>/dev/null || true
	rm -rf "$BIN_DIR" "$DATA_DIR" 2>/dev/null || true
	exit $status
}
