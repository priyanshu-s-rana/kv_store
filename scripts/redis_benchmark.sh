#!/usr/bin/env bash
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
source "${SCRIPT_DIR}/lib/common.sh"

if (( $# < 1 )); then
    echo "Usage: $0 <benchmark-name> [options]"
    echo "Run '$0 --help' for the full option list."
    exit 1
fi

#####################
# Defaults
#####################
HOST="localhost"
PORT="5040"

DEFAULT_REQUESTS=100000
DEFAULT_ITERATIONS=5

CONCURRENCY_LEVELS=(10 50 100 250 500)
PAYLOAD_SIZES=(16 64 256 1024)
KEYSPACES=(1000 10000 100000)
BATCH_SIZES=(2 10 50)
PIPELINE_DEPTHS=(1 16)

REQUESTS="$DEFAULT_REQUESTS"
ITERATIONS="$DEFAULT_ITERATIONS"
NO_REBUILD=false
STRICT_CLEAN=false
QUICK=false

BENCHMARK_NAME=""

usage() {
    cat <<EOF
Usage: $0 <benchmark-name> [options]

  <benchmark-name>            Required. Results go to benchmarks/<name>_<timestamp>/

Options:
  --host <host>                Server host (default: $HOST)
  --port <port>                Server port (default: $PORT)
  --requests <n>                Requests per iteration (default: $DEFAULT_REQUESTS)
  --iterations <n>              Iterations per configuration (default: $DEFAULT_ITERATIONS)
  --concurrency <csv>           Concurrency levels (default: $(IFS=,; echo "${CONCURRENCY_LEVELS[*]}"))
  --payload-sizes <csv>         Payload sizes in bytes (default: $(IFS=,; echo "${PAYLOAD_SIZES[*]}"))
  --keyspaces <csv>             Keyspace sizes (default: $(IFS=,; echo "${KEYSPACES[*]}"))
  --batch-sizes <csv>           MSET/MGET batch sizes (default: $(IFS=,; echo "${BATCH_SIZES[*]}"))
  --pipeline-depths <csv>       Pipeline depths (default: $(IFS=,; echo "${PIPELINE_DEPTHS[*]}"))
  --no-rebuild                  Skip rebuild; reuse a running container if present
  --strict-clean                 Abort instead of warning on a dirty working tree
  --quick                       Smoke-test mode: tiny requests/iterations/single-value dimensions
  -h, --help                    Show this help
EOF
}

#####################
# Pre-scan for --quick / --help so it can set defaults before the main parse loop
#####################
for arg in "$@"; do
    case "$arg" in
        --quick) QUICK=true ;;
        -h|--help) usage; exit 0 ;;
    esac
done

if $QUICK; then
    REQUESTS=1000
    ITERATIONS=1
    CONCURRENCY_LEVELS=(50)
    PAYLOAD_SIZES=(64)
    KEYSPACES=(1000)
    BATCH_SIZES=(2)
    PIPELINE_DEPTHS=(1)
fi

#####################
# Argument parsing
#####################
POSITIONAL=()
while (( $# > 0 )); do
    case "$1" in
        --host)
            HOST="$2"; shift 2 ;;
        --port)
            validate_positive_integer "$1" "$2" || exit 1
            PORT="$2"; shift 2 ;;
        --requests)
            validate_positive_integer "$1" "$2" || exit 1
            REQUESTS="$2"; shift 2 ;;
        --iterations)
            validate_positive_integer "$1" "$2" || exit 1
            ITERATIONS="$2"; shift 2 ;;
        --concurrency)
            validate_csv_of_positive_integers "$1" "$2" || exit 1
            IFS=',' read -r -a CONCURRENCY_LEVELS <<< "$2"; shift 2 ;;
        --payload-sizes)
            validate_csv_of_positive_integers "$1" "$2" || exit 1
            IFS=',' read -r -a PAYLOAD_SIZES <<< "$2"; shift 2 ;;
        --keyspaces)
            validate_csv_of_positive_integers "$1" "$2" || exit 1
            IFS=',' read -r -a KEYSPACES <<< "$2"; shift 2 ;;
        --batch-sizes)
            validate_csv_of_positive_integers "$1" "$2" || exit 1
            IFS=',' read -r -a BATCH_SIZES <<< "$2"; shift 2 ;;
        --pipeline-depths)
            validate_csv_of_positive_integers "$1" "$2" || exit 1
            IFS=',' read -r -a PIPELINE_DEPTHS <<< "$2"; shift 2 ;;
        --no-rebuild)
            NO_REBUILD=true; shift ;;
        --strict-clean)
            STRICT_CLEAN=true; shift ;;
        --quick)
            shift ;;
        -h|--help)
            usage; exit 0 ;;
        --)
            shift; break ;;
        --*)
            echo "Unknown option: $1" >&2; exit 1 ;;
        *)
            POSITIONAL+=("$1"); shift ;;
    esac
done

if (( ${#POSITIONAL[@]} < 1 )); then
    echo "Error: <benchmark-name> is required." >&2
    exit 1
fi
BENCHMARK_NAME="${POSITIONAL[0]}"

# Honors a pre-set RUN_ID (e.g. from `make bench-all`, which shares one
# timestamp across all three suites so results land in one directory)
# instead of always computing a fresh one.
RUN_ID="${RUN_ID:-$(date +"%Y-%m-%d_%H-%M-%S")}"
OUT_DIR="benchmarks/${BENCHMARK_NAME}_${RUN_ID}"
INFO_FILE="${OUT_DIR}/benchmark_info.txt"
FAILURES_FILE="${OUT_DIR}/failures.log"
SUMMARY_FILE="${OUT_DIR}/summary.csv"
SUITE_HAD_FAILURE=0

REPRESENTATIVE_CONCURRENCY="${CONCURRENCY_LEVELS[$(( ${#CONCURRENCY_LEVELS[@]} / 2 ))]}"
DEFAULT_PAYLOAD="${PAYLOAD_SIZES[0]}"
DEFAULT_KEYSPACE="${KEYSPACES[0]}"
DEFAULT_BATCH="${BATCH_SIZES[0]}"

#####################
# Dependency / environment checks
#####################
check_dependency() {
    local missing=()
    command -v docker > /dev/null 2>&1 || missing+=("docker")
    docker compose version > /dev/null 2>&1 || missing+=("docker compose")
    command -v nc > /dev/null 2>&1 || missing+=("nc")
    command -v redis-benchmark > /dev/null 2>&1 || missing+=("redis-benchmark")

    if (( ${#missing[@]} > 0 )); then
        echo "Missing required dependencies:" >&2
        printf ' - %s\n' "${missing[@]}" >&2
        return 1
    fi
}

#####################
# Benchmark helpers
#####################
warm_up() {
    echo "Warming up (10000 SET+GET, discarded)..."
    redis-benchmark -h "$HOST" -p "$PORT" -t set,get -n 10000 -q &> /dev/null \
        || echo "Warm-up failed (continuing anyway)." >&2
}

flush_server() {
    redis-benchmark -h "$HOST" -p "$PORT" -n 1 -c 1 FLUSHALL &> /dev/null \
        || echo "FLUSHALL failed (continuing anyway)." >&2
}

seed_keyspace() {
    local keyspace="$1" payload="$2"
    redis-benchmark -h "$HOST" -p "$PORT" -t set -r "$keyspace" -n "$keyspace" -d "$payload" -q &> /dev/null
}

build_mset_command() {
    local batch="$1" payload="$2"
    local val
    val=$(random_payload "$payload")
    local parts=("MSET")
    local i
    for (( i=0; i<batch; i++ )); do
        parts+=("key:__rand_int__" "$val")
    done
    printf '%s\n' "${parts[*]}"
}

build_mget_command() {
    local batch="$1"
    local parts=("MGET")
    local i
    for (( i=0; i<batch; i++ )); do
        parts+=("key:__rand_int__")
    done
    printf '%s\n' "${parts[*]}"
}

# Parses one redis-benchmark output file. Echoes "rps avg min p50 p95 p99 max"
# on success, returns 1 if the expected blocks aren't present (failure).
parse_iteration_metrics() {
    local file="$1"
    local rps latency_line
    rps=$(grep "throughput summary" "$file" | awk '{print $3}')
    latency_line=$(awk '/latency summary/ { getline; getline; print }' "$file")

    if [[ -z "$rps" || -z "$latency_line" ]]; then
        return 1
    fi

    local latency=()
    read -r -a latency <<< "$latency_line"
    if (( ${#latency[@]} < 6 )); then
        return 1
    fi

    echo "$rps ${latency[0]} ${latency[1]} ${latency[2]} ${latency[3]} ${latency[4]} ${latency[5]}"
}

run_iteration() {
    local output_file="$1"
    shift
    redis-benchmark -h "$HOST" -p "$PORT" -n "$REQUESTS" "$@" &> "$output_file"
}

# run_config <command_label> <concurrency> <payload> <keyspace> <batch> <pipeline> <out_subdir> <config_id> -- <redis-benchmark trailing args...>
run_config() {
    local command_label="$1" concurrency="$2" payload="$3" keyspace="$4" batch="$5" pipeline="$6" out_subdir="$7" config_id="$8"
    shift 8
    [[ "$1" == "--" ]] && shift
    local rb_args=("$@")

    mkdir -p "$out_subdir"

    local rps_list=() avg_list=() min_list=() p50_list=() p95_list=() p99_list=() max_list=()
    local failed=0
    local i

    for (( i=1; i<=ITERATIONS; i++ )); do
        local out_file="${out_subdir}/${config_id}_iter${i}.txt"
        if run_iteration "$out_file" -c "$concurrency" "${rb_args[@]}"; then
            local metrics
            if metrics=$(parse_iteration_metrics "$out_file"); then
                local m_rps m_avg m_min m_p50 m_p95 m_p99 m_max
                read -r m_rps m_avg m_min m_p50 m_p95 m_p99 m_max <<< "$metrics"
                rps_list+=("$m_rps"); avg_list+=("$m_avg"); min_list+=("$m_min")
                p50_list+=("$m_p50"); p95_list+=("$m_p95"); p99_list+=("$m_p99"); max_list+=("$m_max")
            else
                failed=$((failed + 1))
                echo "${command_label},${concurrency},${payload},${keyspace},${batch},${pipeline},${i},parse_failure,${out_file}" >> "$FAILURES_FILE"
            fi
        else
            failed=$((failed + 1))
            echo "${command_label},${concurrency},${payload},${keyspace},${batch},${pipeline},${i},exec_failure,${out_file}" >> "$FAILURES_FILE"
        fi
    done

    if (( failed > 0 )); then
        SUITE_HAD_FAILURE=1
    fi

    local successful=${#rps_list[@]}
    if (( successful == 0 )); then
        echo "Config failed entirely: ${command_label} c=${concurrency} payload=${payload} keyspace=${keyspace} batch=${batch} pipeline=${pipeline}" >&2
        SUITE_HAD_FAILURE=1
        return 0
    fi

    local rps_mean rps_median rps_min rps_max rps_stddev
    local avg_mean avg_median avg_min avg_max avg_stddev
    local min_mean min_median min_min min_max min_stddev
    local p50_mean p50_median p50_min p50_max p50_stddev
    local p95_mean p95_median p95_min p95_max p95_stddev
    local p99_mean p99_median p99_min p99_max p99_stddev
    local max_mean max_median max_min max_max max_stddev

    read -r rps_mean rps_median rps_min rps_max rps_stddev <<< "$(compute_stats "${rps_list[*]}")"
    read -r avg_mean avg_median avg_min avg_max avg_stddev <<< "$(compute_stats "${avg_list[*]}")"
    read -r min_mean min_median min_min min_max min_stddev <<< "$(compute_stats "${min_list[*]}")"
    read -r p50_mean p50_median p50_min p50_max p50_stddev <<< "$(compute_stats "${p50_list[*]}")"
    read -r p95_mean p95_median p95_min p95_max p95_stddev <<< "$(compute_stats "${p95_list[*]}")"
    read -r p99_mean p99_median p99_min p99_max p99_stddev <<< "$(compute_stats "${p99_list[*]}")"
    read -r max_mean max_median max_min max_max max_stddev <<< "$(compute_stats "${max_list[*]}")"

    local ops_sec
    ops_sec=$(awk -v r="$rps_median" -v b="$batch" 'BEGIN { if (b == "-" || b == "") b = 1; printf "%.3f", r * b }')

    echo "${command_label},${concurrency},${rps_median},${avg_median},${min_median},${p50_median},${p95_median},${p99_median},${max_median},${ITERATIONS},${failed},${rps_mean},${rps_stddev},${rps_min},${rps_max},${avg_mean},${avg_stddev},${p50_mean},${p50_stddev},${p95_mean},${p95_stddev},${p99_mean},${p99_stddev},${max_mean},${max_stddev},${payload},${keyspace},${batch},${pipeline},${ops_sec}" >> "$SUMMARY_FILE"
}

#####################
# Benchmark phases
#####################
run_core_sweep() {
    local concurrency
    for concurrency in "${CONCURRENCY_LEVELS[@]}"; do
        local out_subdir="${OUT_DIR}/c${concurrency}"
        echo "Core sweep: concurrency=${concurrency}"
        flush_server

        run_config SET "$concurrency" "$DEFAULT_PAYLOAD" "$DEFAULT_KEYSPACE" - 1 "$out_subdir" "set" -- \
            -t set -d "$DEFAULT_PAYLOAD" -r "$DEFAULT_KEYSPACE"

        seed_keyspace "$DEFAULT_KEYSPACE" "$DEFAULT_PAYLOAD"
        run_config GET "$concurrency" "$DEFAULT_PAYLOAD" "$DEFAULT_KEYSPACE" - 1 "$out_subdir" "get" -- \
            -t get -d "$DEFAULT_PAYLOAD" -r "$DEFAULT_KEYSPACE"

        local mset_cmd mset_args=()
        mset_cmd=$(build_mset_command "$DEFAULT_BATCH" "$DEFAULT_PAYLOAD")
        read -r -a mset_args <<< "$mset_cmd"
        run_config MSET "$concurrency" "$DEFAULT_PAYLOAD" "$DEFAULT_KEYSPACE" "$DEFAULT_BATCH" 1 "$out_subdir" "mset" -- \
            -r "$DEFAULT_KEYSPACE" "${mset_args[@]}"

        seed_keyspace "$DEFAULT_KEYSPACE" "$DEFAULT_PAYLOAD"
        local mget_cmd mget_args=()
        mget_cmd=$(build_mget_command "$DEFAULT_BATCH")
        read -r -a mget_args <<< "$mget_cmd"
        run_config MGET "$concurrency" - "$DEFAULT_KEYSPACE" "$DEFAULT_BATCH" 1 "$out_subdir" "mget" -- \
            -r "$DEFAULT_KEYSPACE" "${mget_args[@]}"

        run_config INCR "$concurrency" - - - 1 "$out_subdir" "incr" -- -t incr

        run_config DECR "$concurrency" - - - 1 "$out_subdir" "decr" -- DECR counter

        flush_server
        seed_keyspace "$DEFAULT_KEYSPACE" "$DEFAULT_PAYLOAD"
        run_config KEYS "$concurrency" - "$DEFAULT_KEYSPACE" - 1 "$out_subdir" "keys" -- KEYS "*"
    done
}

run_payload_sweep() {
    local payload
    local out_subdir="${OUT_DIR}/payload"
    for payload in "${PAYLOAD_SIZES[@]}"; do
        [[ "$payload" == "$DEFAULT_PAYLOAD" ]] && continue
        echo "Payload sweep: payload=${payload}B"
        flush_server

        run_config SET "$REPRESENTATIVE_CONCURRENCY" "$payload" "$DEFAULT_KEYSPACE" - 1 "$out_subdir" "set_p${payload}" -- \
            -t set -d "$payload" -r "$DEFAULT_KEYSPACE"

        seed_keyspace "$DEFAULT_KEYSPACE" "$payload"
        run_config GET "$REPRESENTATIVE_CONCURRENCY" "$payload" "$DEFAULT_KEYSPACE" - 1 "$out_subdir" "get_p${payload}" -- \
            -t get -d "$payload" -r "$DEFAULT_KEYSPACE"
    done
}

run_keyspace_sweep() {
    local keyspace
    local out_subdir="${OUT_DIR}/keyspace"
    for keyspace in "${KEYSPACES[@]}"; do
        [[ "$keyspace" == "$DEFAULT_KEYSPACE" ]] && continue
        echo "Keyspace sweep: keyspace=${keyspace}"
        flush_server

        run_config SET "$REPRESENTATIVE_CONCURRENCY" "$DEFAULT_PAYLOAD" "$keyspace" - 1 "$out_subdir" "set_k${keyspace}" -- \
            -t set -d "$DEFAULT_PAYLOAD" -r "$keyspace"

        seed_keyspace "$keyspace" "$DEFAULT_PAYLOAD"
        run_config GET "$REPRESENTATIVE_CONCURRENCY" "$DEFAULT_PAYLOAD" "$keyspace" - 1 "$out_subdir" "get_k${keyspace}" -- \
            -t get -d "$DEFAULT_PAYLOAD" -r "$keyspace"
    done
}

run_batch_sweep() {
    local batch
    local out_subdir="${OUT_DIR}/batch"
    for batch in "${BATCH_SIZES[@]}"; do
        [[ "$batch" == "$DEFAULT_BATCH" ]] && continue
        echo "Batch sweep: batch=${batch}"
        flush_server

        local mset_cmd mset_args=()
        mset_cmd=$(build_mset_command "$batch" "$DEFAULT_PAYLOAD")
        read -r -a mset_args <<< "$mset_cmd"
        run_config MSET "$REPRESENTATIVE_CONCURRENCY" "$DEFAULT_PAYLOAD" "$DEFAULT_KEYSPACE" "$batch" 1 "$out_subdir" "mset_b${batch}" -- \
            -r "$DEFAULT_KEYSPACE" "${mset_args[@]}"

        seed_keyspace "$DEFAULT_KEYSPACE" "$DEFAULT_PAYLOAD"
        local mget_cmd mget_args=()
        mget_cmd=$(build_mget_command "$batch")
        read -r -a mget_args <<< "$mget_cmd"
        run_config MGET "$REPRESENTATIVE_CONCURRENCY" - "$DEFAULT_KEYSPACE" "$batch" 1 "$out_subdir" "mget_b${batch}" -- \
            -r "$DEFAULT_KEYSPACE" "${mget_args[@]}"
    done
}

run_pipeline_sweep() {
    local pipeline
    local out_subdir="${OUT_DIR}/pipeline"
    for pipeline in "${PIPELINE_DEPTHS[@]}"; do
        [[ "$pipeline" == "1" ]] && continue
        echo "Pipeline sweep: pipeline=${pipeline}"
        flush_server

        run_config SET "$REPRESENTATIVE_CONCURRENCY" "$DEFAULT_PAYLOAD" "$DEFAULT_KEYSPACE" - "$pipeline" "$out_subdir" "set_P${pipeline}" -- \
            -t set -d "$DEFAULT_PAYLOAD" -r "$DEFAULT_KEYSPACE" -P "$pipeline"

        seed_keyspace "$DEFAULT_KEYSPACE" "$DEFAULT_PAYLOAD"
        run_config GET "$REPRESENTATIVE_CONCURRENCY" "$DEFAULT_PAYLOAD" "$DEFAULT_KEYSPACE" - "$pipeline" "$out_subdir" "get_P${pipeline}" -- \
            -t get -d "$DEFAULT_PAYLOAD" -r "$DEFAULT_KEYSPACE" -P "$pipeline"

        run_config INCR "$REPRESENTATIVE_CONCURRENCY" - - - "$pipeline" "$out_subdir" "incr_P${pipeline}" -- \
            -t incr -P "$pipeline"

        run_config DECR "$REPRESENTATIVE_CONCURRENCY" - - - "$pipeline" "$out_subdir" "decr_P${pipeline}" -- \
            -P "$pipeline" DECR counter
    done
}

#####################
# Resource utilization (best-effort, never fails the run)
#####################
capture_resources() {
    local label="$1"
    curl --fail --silent "http://${HOST}:9090/metrics" > "${OUT_DIR}/metrics_${label}.prom" 2>/dev/null \
        || echo "Unable to collect Prometheus metrics (${label})." >&2

    if command -v docker >/dev/null 2>&1; then
        docker stats --no-stream kv-server > "${OUT_DIR}/docker_stats_${label}.txt" 2>/dev/null \
            || echo "Unable to collect docker stats (${label})." >&2
    fi
}

#####################
# Main
#####################
check_dependency || exit 1

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

mkdir -p "$OUT_DIR"
: > "$SUMMARY_FILE"
: > "$FAILURES_FILE"

ensure_server || exit 1

record_env "$INFO_FILE" \
    "Image Digest: ${BUILT_DIGEST:-N/A}" \
    "Container Image ID: ${CONTAINER_IMAGE_ID:-N/A}" \
    "Container ID: $(docker compose ps -q kv-server)" \
    "Docker: $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo N/A)" \
    "Compose Version: $(docker compose version --short 2>/dev/null || echo N/A)" \
    "Requests: $REQUESTS" \
    "Iterations: $ITERATIONS" \
    "Concurrency Levels: ${CONCURRENCY_LEVELS[*]}" \
    "Payload Sizes: ${PAYLOAD_SIZES[*]}" \
    "Keyspaces: ${KEYSPACES[*]}" \
    "Batch Sizes: ${BATCH_SIZES[*]}" \
    "Pipeline Depths: ${PIPELINE_DEPTHS[*]}" \
    "Rebuild: $(! $NO_REBUILD && echo true || echo false)" \
    "Warmup: yes (10000 set+get)" \
    "Quick Mode: $QUICK"

warm_up

{
    echo "#schema=2"
    echo "Command,Concurrency,rps,avg,min,p50,P95,P99,Max,Iterations,FailedIterations,rps_mean,rps_stddev,rps_min,rps_max,avg_mean,avg_stddev,p50_mean,p50_stddev,p95_mean,p95_stddev,p99_mean,p99_stddev,max_mean,max_stddev,Payload,Keyspace,Batch,Pipeline,OpsSec"
} > "$SUMMARY_FILE"

echo "Running benchmark '${BENCHMARK_NAME}': requests=${REQUESTS}, iterations=${ITERATIONS}"
echo "Concurrency levels: ${CONCURRENCY_LEVELS[*]}"

capture_resources "before"

run_core_sweep
run_payload_sweep
run_keyspace_sweep
run_batch_sweep
run_pipeline_sweep

capture_resources "after"

if command -v column >/dev/null 2>&1; then
    echo
    echo "Summary:"
    tail -n +2 "$SUMMARY_FILE" | column -t -s,
fi

echo
if (( SUITE_HAD_FAILURE == 1 )); then
    echo "Benchmark completed WITH FAILURES. See ${FAILURES_FILE}"
    echo "Results saved to $OUT_DIR"
    exit 1
fi

echo "Benchmark completed."
echo "Results saved to $OUT_DIR"
