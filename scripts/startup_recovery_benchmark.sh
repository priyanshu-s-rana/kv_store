#!/usr/bin/env bash
set -uo pipefail

# Startup/recovery benchmark: times how long the server takes to go
# from process start to accept-connections-ready, for an empty DB,
# snapshot-only recovery, and snapshot+journal-replay recovery.
#
# Runs against an ISOLATED docker compose project (its own container,
# ports, and named volume — see lib/docker-compose.startup-bench.yml)
# so it never touches a developer's regular `docker compose up`
# instance or its data volume, even if that's running concurrently.
#
# Mechanism notes (verified against the code, not assumed):
#   - cmd/kv-server/main.go runs persist.Recovery() BEFORE
#     server.Start() binds the listener. So "how long until the port
#     accepts connections" IS the recovery time — no log-scraping needed.
#   - REBASELINE (store/commands.go) synchronously snapshots and
#     rotates the journal — used here to force a clean, journal-empty
#     snapshot before the "snapshot-only" scenario.
#   - The server accepts plain inline commands over a raw TCP
#     connection (see the Dockerfile HEALTHCHECK: `echo PING | nc`),
#     so REBASELINE can be sent the same way without a Redis client.
#   - After a recovery that replayed a non-empty journal, the server
#     auto-rebaselines — so a *second* restart against leftover state
#     would silently degrade into a snapshot-only measurement. To keep
#     iterations independent, every iteration tears down and rebuilds
#     its dataset from scratch rather than reusing state.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
source "${SCRIPT_DIR}/lib/common.sh"

COMPOSE_FILE="${SCRIPT_DIR}/lib/docker-compose.startup-bench.yml"
PROJECT_NAME="kv-bench-startup"
SERVICE="kv-server-bench"

if (( $# < 1 )); then
    echo "Usage: $0 <benchmark-name> [options]"
    echo "Run '$0 --help' for the full option list."
    exit 1
fi

BENCH_HOST="localhost"
BENCH_PORT="5041"
ITERATIONS=3
SMALL_KEYS=1000
MEDIUM_KEYS=50000
LARGE_KEYS=500000
EXTRA_WRITES=5000
PAYLOAD=64
NO_REBUILD=false
STRICT_CLEAN=false
QUICK=false

usage() {
    cat <<EOF
Usage: $0 <benchmark-name> [options]

  <benchmark-name>          Required. Results go to benchmarks/<name>_<timestamp>/startup_recovery/

Options:
  --port <port>              Host port for the isolated bench container (default: $BENCH_PORT)
  --iterations <n>            Iterations per scenario, reports median (default: $ITERATIONS)
  --small-keys <n>            (default: $SMALL_KEYS)
  --medium-keys <n>           (default: $MEDIUM_KEYS)
  --large-keys <n>            (default: $LARGE_KEYS)
  --extra-writes <n>          Post-checkpoint writes for journal-replay scenarios (default: $EXTRA_WRITES)
  --payload-size <bytes>       (default: $PAYLOAD)
  --no-rebuild                Skip rebuild; use whatever kv_store:latest image already exists
  --strict-clean               Abort instead of warning on a dirty working tree
  --quick                     Smoke-test mode: 1 iteration, tiny datasets
  -h, --help                  Show this help
EOF
}

for arg in "$@"; do
    case "$arg" in
        --quick) QUICK=true ;;
        -h|--help) usage; exit 0 ;;
    esac
done
if $QUICK; then
    ITERATIONS=1
    SMALL_KEYS=100
    MEDIUM_KEYS=1000
    LARGE_KEYS=5000
    EXTRA_WRITES=200
fi

POSITIONAL=()
while (( $# > 0 )); do
    case "$1" in
        --port) validate_positive_integer "$1" "$2" || exit 1; BENCH_PORT="$2"; shift 2 ;;
        --iterations) validate_positive_integer "$1" "$2" || exit 1; ITERATIONS="$2"; shift 2 ;;
        --small-keys) validate_positive_integer "$1" "$2" || exit 1; SMALL_KEYS="$2"; shift 2 ;;
        --medium-keys) validate_positive_integer "$1" "$2" || exit 1; MEDIUM_KEYS="$2"; shift 2 ;;
        --large-keys) validate_positive_integer "$1" "$2" || exit 1; LARGE_KEYS="$2"; shift 2 ;;
        --extra-writes) validate_positive_integer "$1" "$2" || exit 1; EXTRA_WRITES="$2"; shift 2 ;;
        --payload-size) validate_positive_integer "$1" "$2" || exit 1; PAYLOAD="$2"; shift 2 ;;
        --no-rebuild) NO_REBUILD=true; shift ;;
        --strict-clean) STRICT_CLEAN=true; shift ;;
        --quick) shift ;;
        -h|--help) usage; exit 0 ;;
        --*) echo "Unknown option: $1" >&2; exit 1 ;;
        *) POSITIONAL+=("$1"); shift ;;
    esac
done

if (( ${#POSITIONAL[@]} < 1 )); then
    echo "Error: <benchmark-name> is required." >&2
    exit 1
fi
BENCHMARK_NAME="${POSITIONAL[0]}"

for dep in docker nc redis-benchmark; do
    command -v "$dep" > /dev/null 2>&1 || { echo "Missing required dependency: $dep" >&2; exit 1; }
done
docker compose version > /dev/null 2>&1 || { echo "Missing required dependency: docker compose" >&2; exit 1; }

if git_is_dirty; then
    if $STRICT_CLEAN; then
        echo "ERROR: working tree is dirty and --strict-clean was given. Commit or stash first." >&2
        exit 1
    else
        echo "WARNING: working tree is dirty. Results will not map cleanly to a single commit." >&2
    fi
fi

if ! check_docker; then
    echo "Docker daemon is not running." >&2
    exit 1
fi

# Honors a pre-set RUN_ID (e.g. from `make bench-all`, which shares one
# timestamp across all three suites so results land in one directory)
# instead of always computing a fresh one.
RUN_ID="${RUN_ID:-$(date +"%Y-%m-%d_%H-%M-%S")}"
OUT_DIR="benchmarks/${BENCHMARK_NAME}_${RUN_ID}/startup_recovery"
INFO_FILE="${OUT_DIR}/benchmark_info.txt"
SUMMARY_FILE="${OUT_DIR}/summary.csv"
mkdir -p "$OUT_DIR"

dc() {
    docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" "$@"
}

cleanup() {
    dc down -v --remove-orphans > /dev/null 2>&1 || true
}
trap cleanup EXIT

# Polls until the bench port accepts connections; echoes elapsed
# seconds (~50ms resolution via iteration-count, portable across
# GNU/BSD date which disagree on sub-second %N support).
time_until_ready() {
    local interval="0.05"
    local max_wait=120
    local count=0
    local max_iters
    max_iters=$(awk -v w="$max_wait" -v i="$interval" 'BEGIN{printf "%d", w / i}')
    while ! nc -z "$BENCH_HOST" "$BENCH_PORT" 2>/dev/null; do
        sleep "$interval"
        count=$((count + 1))
        if (( count >= max_iters )); then
            echo "Timed out waiting for server to become ready." >&2
            return 1
        fi
    done
    awk -v c="$count" -v i="$interval" 'BEGIN{printf "%.3f", c * i}'
}

send_command() {
    printf '%s\r\n' "$1" | nc -w 5 "$BENCH_HOST" "$BENCH_PORT" > /dev/null
}

seed_keys() {
    local n="$1"
    redis-benchmark -h "$BENCH_HOST" -p "$BENCH_PORT" -t set -r "$n" -n "$n" -d "$PAYLOAD" -q &> /dev/null
}

fresh_start() {
    dc down -v --remove-orphans > /dev/null 2>&1 || true
    dc up -d "$SERVICE" > /dev/null
    time_until_ready > /dev/null || return 1
}

restart_and_time() {
    dc stop "$SERVICE" > /dev/null
    dc up -d "$SERVICE" > /dev/null
    time_until_ready
}

# run_scenario <name> <dataset_keys> <journal_writes> <setup_fn>
run_scenario() {
    local name="$1" dataset_keys="$2" journal_writes="$3" setup_fn="$4"
    echo "Scenario: $name (dataset=${dataset_keys} journal=${journal_writes})"

    local times=()
    local i
    for (( i=1; i<=ITERATIONS; i++ )); do
        if ! fresh_start; then
            echo "  iter $i: failed to bring up a fresh container, skipping" >&2
            continue
        fi
        "$setup_fn" "$dataset_keys" "$journal_writes"

        local elapsed
        if elapsed=$(restart_and_time); then
            echo "  iter $i: ${elapsed}s"
            times+=("$elapsed")
        else
            echo "  iter $i: recovery timed out" >&2
        fi
    done

    if (( ${#times[@]} == 0 )); then
        echo "  all iterations failed for $name" >&2
        echo "${name},${dataset_keys},${journal_writes},${PAYLOAD},${ITERATIONS},,,,," >> "$SUMMARY_FILE"
        return
    fi

    local mean median min max stddev
    read -r mean median min max stddev <<< "$(compute_stats "${times[*]}")"
    echo "${name},${dataset_keys},${journal_writes},${PAYLOAD},${#times[@]},${median},${mean},${stddev},${min},${max}" >> "$SUMMARY_FILE"
}

setup_empty() {
    :
}

setup_snapshot_only() {
    local n="$1"
    seed_keys "$n"
    send_command "REBASELINE"
    sleep 1
}

setup_snapshot_plus_journal() {
    local n="$1" extra="$2"
    seed_keys "$n"
    send_command "REBASELINE"
    sleep 1
    seed_keys "$extra"
}

echo "Building bench image..."
if ! $NO_REBUILD; then
    dc build "$SERVICE" || { echo "Docker build failed." >&2; exit 1; }
fi
BUILT_DIGEST=$(capture_image_digest kv_store-bench)

record_env "$INFO_FILE" \
    "Image Digest: ${BUILT_DIGEST:-N/A}" \
    "Bench Port: $BENCH_PORT" \
    "Iterations: $ITERATIONS" \
    "Small Keys: $SMALL_KEYS" \
    "Medium Keys: $MEDIUM_KEYS" \
    "Large Keys: $LARGE_KEYS" \
    "Extra Writes: $EXTRA_WRITES" \
    "Payload: $PAYLOAD" \
    "Quick Mode: $QUICK"

{
    echo "#schema=1"
    echo "Scenario,DatasetKeys,JournalWrites,PayloadBytes,Iterations,RecoveryTimeMedian,RecoveryTimeMean,RecoveryTimeStddev,RecoveryTimeMin,RecoveryTimeMax"
} > "$SUMMARY_FILE"

run_scenario "empty" 0 0 setup_empty
run_scenario "snapshot_small" "$SMALL_KEYS" 0 setup_snapshot_only
run_scenario "snapshot_medium" "$MEDIUM_KEYS" 0 setup_snapshot_only
run_scenario "snapshot_large" "$LARGE_KEYS" 0 setup_snapshot_only
run_scenario "snapshot_journal_small" "$SMALL_KEYS" "$EXTRA_WRITES" setup_snapshot_plus_journal
run_scenario "snapshot_journal_medium" "$MEDIUM_KEYS" "$EXTRA_WRITES" setup_snapshot_plus_journal
run_scenario "snapshot_journal_large" "$LARGE_KEYS" "$EXTRA_WRITES" setup_snapshot_plus_journal

echo
echo "Summary:"
if command -v column >/dev/null 2>&1; then
    tail -n +2 "$SUMMARY_FILE" | column -t -s,
fi

echo
echo "Startup/recovery benchmark completed."
echo "Results saved to $OUT_DIR"
