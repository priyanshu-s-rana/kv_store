#!/usr/bin/env bash
set -uo pipefail

# Macrobenchmark suite using memtier_benchmark for realistic mixed
# read/write workloads over a sustained duration. Separate from
# redis_benchmark.sh (redis-benchmark) — that script does isolated,
# fixed-request-count command microbenchmarks; this one does
# duration-based mixed-ratio load, which is where GC behavior, heap
# growth, and latency drift under sustained load actually show up.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
source "${SCRIPT_DIR}/lib/common.sh"

if (( $# < 1 )); then
    echo "Usage: $0 <benchmark-name> [options]"
    echo "Run '$0 --help' for the full option list."
    exit 1
fi

HOST="localhost"
PORT="5040"

RATIO_PRESET="cache-heavy"
PAYLOAD_SIZE=64
KEYSPACE=10000
CLIENTS=50
THREADS=4
TEST_TIME=60
PIPELINE=1
KEY_PATTERN="uniform"
NO_REBUILD=false
STRICT_CLEAN=false
QUICK=false

usage() {
    cat <<EOF
Usage: $0 <benchmark-name> [options]

  <benchmark-name>            Required. Results go to benchmarks/<name>_<timestamp>/macro/

Options:
  --host <host>                Server host (default: $HOST)
  --port <port>                Server port (default: $PORT)
  --ratio <preset|S:G>         cache-heavy (5:95 set:get), balanced (30:70),
                                write-heavy (60:40), or a raw memtier S:G ratio
                                (default: $RATIO_PRESET)
  --payload-size <bytes>        (default: $PAYLOAD_SIZE)
  --keyspace <n>                 (default: $KEYSPACE)
  --clients <n>                  Clients per thread (default: $CLIENTS)
  --threads <n>                  (default: $THREADS)
  --test-time <seconds>          Duration of the run (default: $TEST_TIME)
  --pipeline <depth>             (default: $PIPELINE)
  --key-pattern <uniform|gaussian|sequential>  (default: $KEY_PATTERN)
  --no-rebuild                   Skip rebuild; reuse a running container if present
  --strict-clean                  Abort instead of warning on a dirty working tree
  --quick                        Smoke-test mode: short test-time, tiny client/keyspace counts
  -h, --help                     Show this help

Note: memtier_benchmark does not support Zipfian key distributions;
only uniform, gaussian, and sequential are available.
EOF
}

for arg in "$@"; do
    case "$arg" in
        --quick) QUICK=true ;;
        -h|--help) usage; exit 0 ;;
    esac
done
if $QUICK; then
    TEST_TIME=10
    CLIENTS=5
    THREADS=1
    KEYSPACE=100
    PAYLOAD_SIZE=64
fi

POSITIONAL=()
while (( $# > 0 )); do
    case "$1" in
        --host) HOST="$2"; shift 2 ;;
        --port) validate_positive_integer "$1" "$2" || exit 1; PORT="$2"; shift 2 ;;
        --ratio) RATIO_PRESET="$2"; shift 2 ;;
        --payload-size) validate_positive_integer "$1" "$2" || exit 1; PAYLOAD_SIZE="$2"; shift 2 ;;
        --keyspace) validate_positive_integer "$1" "$2" || exit 1; KEYSPACE="$2"; shift 2 ;;
        --clients) validate_positive_integer "$1" "$2" || exit 1; CLIENTS="$2"; shift 2 ;;
        --threads) validate_positive_integer "$1" "$2" || exit 1; THREADS="$2"; shift 2 ;;
        --test-time) validate_positive_integer "$1" "$2" || exit 1; TEST_TIME="$2"; shift 2 ;;
        --pipeline) validate_positive_integer "$1" "$2" || exit 1; PIPELINE="$2"; shift 2 ;;
        --key-pattern) KEY_PATTERN="$2"; shift 2 ;;
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

case "$RATIO_PRESET" in
    cache-heavy) MEMTIER_RATIO="1:19" ;;   # 5% set / 95% get
    balanced)    MEMTIER_RATIO="3:7"  ;;   # 30% set / 70% get
    write-heavy) MEMTIER_RATIO="3:2"  ;;   # 60% set / 40% get
    *:*)         MEMTIER_RATIO="$RATIO_PRESET" ;;
    *)
        echo "Error: --ratio must be cache-heavy, balanced, write-heavy, or a raw S:G ratio." >&2
        exit 1 ;;
esac

case "$KEY_PATTERN" in
    uniform)    MEMTIER_KEY_PATTERN="R:R" ;;
    gaussian)   MEMTIER_KEY_PATTERN="G:G" ;;
    sequential) MEMTIER_KEY_PATTERN="S:S" ;;
    *)
        echo "Error: --key-pattern must be uniform, gaussian, or sequential." >&2
        exit 1 ;;
esac

if ! command -v memtier_benchmark > /dev/null 2>&1; then
    echo "memtier_benchmark not found — skipping macro suite."
    echo "Install: https://github.com/RedisLabs/memtier_benchmark#installation"
    exit 0
fi

if ! command -v docker > /dev/null 2>&1 || ! docker compose version > /dev/null 2>&1; then
    echo "Missing required dependency: docker / docker compose." >&2
    exit 1
fi

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
OUT_DIR="benchmarks/${BENCHMARK_NAME}_${RUN_ID}/macro"
INFO_FILE="${OUT_DIR}/benchmark_info.txt"
SUMMARY_FILE="${OUT_DIR}/summary.csv"
RAW_OUT="${OUT_DIR}/memtier_raw.txt"
mkdir -p "$OUT_DIR"

ensure_server || exit 1

record_env "$INFO_FILE" \
    "Image Digest: ${BUILT_DIGEST:-N/A}" \
    "Container Image ID: ${CONTAINER_IMAGE_ID:-N/A}" \
    "Container ID: $(docker compose ps -q kv-server)" \
    "Ratio Preset: $RATIO_PRESET ($MEMTIER_RATIO set:get)" \
    "Payload Size: $PAYLOAD_SIZE" \
    "Keyspace: $KEYSPACE" \
    "Clients: $CLIENTS" \
    "Threads: $THREADS" \
    "Test Time: ${TEST_TIME}s" \
    "Pipeline: $PIPELINE" \
    "Key Pattern: $KEY_PATTERN ($MEMTIER_KEY_PATTERN)"

echo "#schema=1" > "$SUMMARY_FILE"
echo "RatioPreset,Ratio,Payload,Keyspace,Clients,Threads,TestTime,Pipeline,KeyPattern,OpsSec,AvgLatency,P50,P99,P999" >> "$SUMMARY_FILE"

curl --fail --silent "http://${HOST}:9090/metrics" > "${OUT_DIR}/metrics_before.prom" 2>/dev/null \
    || echo "Unable to collect Prometheus metrics." >&2

echo "Running memtier_benchmark: ratio=${RATIO_PRESET} (${MEMTIER_RATIO}) payload=${PAYLOAD_SIZE}B keyspace=${KEYSPACE} clients=${CLIENTS} threads=${THREADS} test-time=${TEST_TIME}s pipeline=${PIPELINE} key-pattern=${KEY_PATTERN}"

if ! memtier_benchmark \
    -s "$HOST" -p "$PORT" \
    --protocol=redis \
    --ratio="$MEMTIER_RATIO" \
    -d "$PAYLOAD_SIZE" \
    --key-minimum=1 --key-maximum="$KEYSPACE" \
    --key-pattern="$MEMTIER_KEY_PATTERN" \
    -c "$CLIENTS" -t "$THREADS" \
    --test-time="$TEST_TIME" \
    --pipeline="$PIPELINE" \
    --hide-histogram \
    &> "$RAW_OUT"; then
    echo "memtier_benchmark run failed. See $RAW_OUT" >&2
    exit 1
fi

curl --fail --silent "http://${HOST}:9090/metrics" > "${OUT_DIR}/metrics_after.prom" 2>/dev/null \
    || echo "Unable to collect Prometheus metrics." >&2

if command -v docker >/dev/null 2>&1; then
    docker stats --no-stream kv-server > "${OUT_DIR}/docker_stats_after.txt" 2>/dev/null \
        || echo "Unable to collect docker stats." >&2
fi

# Parses memtier's "ALL STATS" Totals row:
#   Type  Ops/sec  Hits/sec  Misses/sec  Avg.Latency  p50  p99  p99.9  KB/sec
TOTALS_LINE=$(grep -E '^Totals' "$RAW_OUT")
if [[ -z "$TOTALS_LINE" ]]; then
    echo "Could not find a Totals line in memtier output ($RAW_OUT) — leaving summary.csv metric columns blank." >&2
    echo "${RATIO_PRESET},${MEMTIER_RATIO},${PAYLOAD_SIZE},${KEYSPACE},${CLIENTS},${THREADS},${TEST_TIME},${PIPELINE},${KEY_PATTERN},,,,," >> "$SUMMARY_FILE"
    exit 1
fi

read -r _ ops_sec _ _ avg_latency p50 p99 p999 _ <<< "$TOTALS_LINE"
echo "${RATIO_PRESET},${MEMTIER_RATIO},${PAYLOAD_SIZE},${KEYSPACE},${CLIENTS},${THREADS},${TEST_TIME},${PIPELINE},${KEY_PATTERN},${ops_sec},${avg_latency},${p50},${p99},${p999}" >> "$SUMMARY_FILE"

echo
echo "Summary:"
if command -v column >/dev/null 2>&1; then
    tail -n +2 "$SUMMARY_FILE" | column -t -s,
fi

echo
echo "Macro benchmark completed."
echo "Results saved to $OUT_DIR"
