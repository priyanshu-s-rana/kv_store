#!/usr/bin/env bash

if (( $# < 1 )); then
    echo "Usage: $0 <benchmark-name> [--requests <n>]"
    exit 1
fi

HOST="localhost"
PORT="5040"

DEFAULT_REQUESTS=100000

REQUESTS="$DEFAULT_REQUESTS"

RUN_ID=$(date +"%Y-%m-%d_%H-%M-%S")
OUT_DIR="benchmarks/$RUN_ID"
INFO_FILE="${OUT_DIR}/benchmark_info.txt"
mkdir -p "$OUT_DIR"

SUMMARY_FILE="$OUT_DIR/summary.csv"
echo "Command,Concurrency,rps,avg,min,p50,P95,P99,Max" > "$SUMMARY_FILE"

BENCHMARK_COMMANDS=(
    "SET foo bar"
    "GET foo"
    "MSET k1 v1 k2 v2"
    "MGET k1 k2"
    "INCR counter"
    "DECR counter"
    "KEYS *"
)

CONCURRENCY_LEVELS=(
    10
    50
    100
    250
    500
)

validate_positive_integer() {
    local option="$1"
    local value="$2"

    if [[ -z "$value" || "$value" == --* ]]; then
        echo "Error: $option requires a value."
        return 1
    fi

    if ! [[ "$value" =~ ^[0-9]+$ ]]; then
        echo "Error: $option must be a positive integer."
        return 1
    fi
}

check_dependency() {
    local missing=()
    command -v docker > /dev/null 2>&1 || missing+=("docker")
    docker compose version > /dev/null 2>&1 || missing+=("docker compose")
    command -v nc > /dev/null 2>&1 || missing+=("nc")
    command -v redis-benchmark > /dev/null 2>&1 || missing+=("redis-benchmark")

    if (( ${#missing[@]} > 0 )); then
        echo "Missing required dependencies"
        printf ' - %s\n' "${missing[@]}"
        return 1
    fi
}

record_env() {
    OS="$(uname)"
    
    if [[ "$OS" == "Linux" ]]; then
        CPU_MODEL=$(lscpu | awk -F: '/Model name/ {gsub(/^[ \t]+/, "", $2); print $2}')
        TOTAL_MEM=$(free -h | awk '/Mem:/ {print $2}')
    elif [[ "$OS" == "Darwin" ]]; then
        CPU_MODEL=$(sysctl -n machdep.cpu.brand_string)
        TOTAL_MEM=$(sysctl -n hw.memsize)
        TOTAL_MEM="$((TOTAL_MEM / 1024 / 1024 / 1024)) GB"
    else
        CPU_MODEL="Unknown"
        TOTAL_MEM="Unknown"
    fi

    {
        echo "=== Benchmark Information ==="
        echo "Date: $(date)"
        echo "Commit: $(git rev-parse HEAD)"
        echo "Branch: $(git branch --show-current)"
        echo "Go: $(go version)"
        echo "OS: $(uname -a)"
        echo "Docker: $(docker version --format '{{.Server.Version}}')"
        echo "Compose Version: $(docker compose version --short)"
        echo "Image: $(docker image inspect kv_store:latest --format '{{.Id}}' 2>/dev/null || echo N/A)"
        echo "Container: $(docker compose ps -q kv-server)"
        echo "CPU: $CPU_MODEL"
        echo "Memory: $TOTAL_MEM"
        echo "Requests: $REQUESTS"
        echo "Concurrency Levels: ${CONCURRENCY_LEVELS[*]}"
    } > "$INFO_FILE"
}

check_docker() {
    docker info > /dev/null 2>&1 || return 1
}

check_kv_server() {
    docker compose ps --status running --services | grep -qx "kv-server"
}

start_kv_server() {
    docker compose up --build -d
}

wait_for_server() {
    local timeout=30
    echo "Waiting for KV Server..."
    while ! nc -z "$HOST" "$PORT"; do
        ((timeout--))
        if (( timeout == 0 )); then
            echo "Timed out waiting for server."
            return 1
        fi

        sleep 1
    done

    echo "KV Server is ready."
}

append_summary() {
    local file="$1"
    local command="$2"
    local concurrency="$3"
    local rps=$(grep "throughput summary" "$file" | awk '{print $3}')
    local latency_line=$(awk '/latency summary/ {
        getline
        getline
        print
    }' "$file")
    local latency=()
    read -r -a latency <<< "$latency_line"
    local avg="${latency[0]}"
    local min="${latency[1]}"
    local p50="${latency[2]}"
    local p95="${latency[3]}"
    local p99="${latency[4]}"
    local max="${latency[5]}"
    

    echo "$command,$concurrency,$rps,$avg,$min,$p50,$p95,$p99,$max" >> "$SUMMARY_FILE"
}

run_benchmark() {
    local command="$1"
    local concurrency="$2"
    local args=()
    read -r -a args <<< "$command"
    local command_name=$(printf '%s' "${args[0]}" | tr '[:upper:]' '[:lower:]')
    local output_file="$OUT_DIR/c${concurrency}/${command_name}.txt"
    echo "Running ${command_name} benchmark..."

    if redis-benchmark \
        -h "$HOST" \
        -p "$PORT" \
        -n "$REQUESTS" \
        -c "$concurrency" \
        "${args[@]}" \
        &> "$output_file"; then
        echo "Finished ${command_name} benchmark."
        append_summary "$output_file" "$command_name" "$concurrency"
    else
        echo "Failed ${command_name} benchmark."
        return 1
    fi
}

run_all_benchmarks() {
    local command
    local concurrency
    for concurrency in "${CONCURRENCY_LEVELS[@]}"; do
        mkdir -p "${OUT_DIR}/c${concurrency}"
        echo "Running benchmarks for concurrency: ${concurrency}"
        for command in "${BENCHMARK_COMMANDS[@]}"; do
            run_benchmark "$command" "$concurrency" || return 1
        done
    done
}

#####################
# Main
#####################
while (( $# > 0)); do
    case "$1" in
        --requests)
            validate_positive_integer "$1" "$2" || exit 1
            REQUESTS="$2"
            shift 2
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

check_dependency || exit 1

record_env

if ! check_docker; then
    echo "Docker daemon is not running."
    exit 1
fi

if ! check_kv_server; then
    echo "Starting KV server..."
    if ! start_kv_server; then
        echo "Unable to start KV server..."
        exit 1
    fi

    wait_for_server || exit 1
fi

echo "Running benchmark for requests=${REQUESTS}"
echo "Concurrency levels: ${CONCURRENCY_LEVELS[*]}"

curl --fail --silent http://localhost:9090/metrics \
    > "${OUT_DIR}/metrics_before.prom" \
    || echo "Unable to collect Prometheus metrics."

run_all_benchmarks || exit 1

curl --fail --silent http://localhost:9090/metrics \
    > "${OUT_DIR}/metrics_after.prom" \
    || echo "Unable to collect Prometheus metrics."

if command -v column >/dev/null 2>&1; then
    echo
    echo "Summary:"
    column -t -s, "$SUMMARY_FILE"
fi

echo
echo "Benchmark completed."
echo "Results saved to $OUT_DIR"
