#!/usr/bin/env bash
# shellcheck shell=bash
# Shared helpers for redis_benchmark.sh, compare.sh, memtier_benchmark.sh,
# startup_recovery_benchmark.sh. Sourced, not executed.

validate_positive_integer() {
    local option="$1"
    local value="$2"

    if [[ -z "$value" || "$value" == --* ]]; then
        echo "Error: $option requires a value." >&2
        return 1
    fi

    if ! [[ "$value" =~ ^[0-9]+$ ]]; then
        echo "Error: $option must be a positive integer." >&2
        return 1
    fi
}

validate_csv_of_positive_integers() {
    local option="$1"
    local value="$2"
    local item

    if [[ -z "$value" ]]; then
        echo "Error: $option requires a value." >&2
        return 1
    fi

    IFS=',' read -r -a _csv_items <<< "$value"
    for item in "${_csv_items[@]}"; do
        if ! [[ "$item" =~ ^[0-9]+$ ]]; then
            echo "Error: $option must be a comma-separated list of positive integers (got '$item')." >&2
            return 1
        fi
    done
}

# Emits "mean median min max stddev" (population stddev) for a
# whitespace-separated list of numbers passed as a single string arg.
compute_stats() {
    local values="$1"
    awk -v vals="$values" '
        BEGIN {
            n = split(vals, a, " ")
            if (n == 0) { print "0 0 0 0 0"; exit }
            sum = 0
            min = a[1]
            max = a[1]
            for (i = 1; i <= n; i++) {
                sum += a[i]
                if (a[i] < min) min = a[i]
                if (a[i] > max) max = a[i]
            }
            mean = sum / n

            # sort for median
            for (i = 1; i <= n; i++) sorted[i] = a[i]
            for (i = 1; i <= n; i++) {
                for (j = i + 1; j <= n; j++) {
                    if (sorted[j] < sorted[i]) {
                        tmp = sorted[i]; sorted[i] = sorted[j]; sorted[j] = tmp
                    }
                }
            }
            if (n % 2 == 1) {
                median = sorted[(n + 1) / 2]
            } else {
                median = (sorted[n / 2] + sorted[n / 2 + 1]) / 2
            }

            sq = 0
            for (i = 1; i <= n; i++) sq += (a[i] - mean) ^ 2
            stddev = (n > 0) ? sqrt(sq / n) : 0

            printf "%.3f %.3f %.3f %.3f %.3f\n", mean, median, min, max, stddev
        }
    '
}

git_is_dirty() {
    [[ -n "$(git status --porcelain 2>/dev/null)" ]]
}

git_commit() {
    git rev-parse HEAD 2>/dev/null || echo "unknown"
}

git_branch() {
    git branch --show-current 2>/dev/null || echo "unknown"
}

logical_cpu_count() {
    if [[ "$(uname)" == "Darwin" ]]; then
        sysctl -n hw.ncpu
    else
        nproc 2>/dev/null || grep -c ^processor /proc/cpuinfo
    fi
}

# Generates a whitespace-free random payload of exactly $1 bytes.
# Only newlines are stripped (base64's own line-wrapping) — '+', '/',
# '=' are all whitespace-safe for shell word-splitting, and stripping
# them would shrink the output below the requested size for larger
# payloads.
random_payload() {
    local size="$1"
    # base64 expands ~4/3, so over-request raw bytes then trim to size.
    local raw_bytes=$(( size * 3 / 4 + 4 ))
    head -c "$raw_bytes" /dev/urandom | base64 | tr -d '\n' | head -c "$size"
}

capture_image_digest() {
    local image="$1"
    docker image inspect "$image" --format '{{.Id}}' 2>/dev/null || echo "N/A"
}

capture_container_image_id() {
    local container="$1"
    docker inspect "$container" --format '{{.Image}}' 2>/dev/null || echo "N/A"
}

# Writes extended benchmark metadata to $1. Remaining args are
# "Key: Value" lines appended verbatim after the common block, so
# each script can add its own parameters.
record_env() {
    local info_file="$1"
    shift
    local extra_lines=("$@")

    local os cpu_model total_mem kernel
    os="$(uname)"

    if [[ "$os" == "Linux" ]]; then
        cpu_model=$(lscpu | awk -F: '/Model name/ {gsub(/^[ \t]+/, "", $2); print $2}')
        total_mem=$(free -h | awk '/Mem:/ {print $2}')
        kernel="$(uname -r)"
    elif [[ "$os" == "Darwin" ]]; then
        cpu_model=$(sysctl -n machdep.cpu.brand_string)
        total_mem=$(sysctl -n hw.memsize)
        total_mem="$((total_mem / 1024 / 1024 / 1024)) GB"
        kernel="$(uname -r)"
    else
        cpu_model="Unknown"
        total_mem="Unknown"
        kernel="Unknown"
    fi

    {
        echo "=== Benchmark Information ==="
        echo "Date: $(date)"
        echo "Commit: $(git_commit)"
        echo "Branch: $(git_branch)"
        echo "Dirty Working Tree: $(git_is_dirty && echo true || echo false)"
        echo "Go: $(go version 2>/dev/null || echo N/A)"
        echo "OS: $(uname -a)"
        echo "Kernel: $kernel"
        echo "CPU: $cpu_model"
        echo "Logical CPUs: $(logical_cpu_count)"
        echo "Memory: $total_mem"
        printf '%s\n' "${extra_lines[@]}"
    } > "$info_file"
}

#####################
# Docker / server lifecycle
#
# Callers must set HOST, PORT, NO_REBUILD (bool) as globals before
# sourcing this section's functions. Sets CONTAINER_IMAGE_ID and
# BUILT_DIGEST as globals for the caller to read afterward.
#####################
check_docker() {
    docker info > /dev/null 2>&1
}

check_kv_server() {
    docker compose ps --status running --services | grep -qx "kv-server"
}

# Application-level readiness check. A bare TCP connect (nc -z) can
# succeed before the app inside is actually ready: on Docker Desktop
# for Mac, the host-side port-forwarding proxy accepts connections as
# soon as the container starts, before the server process has finished
# recovery and bound its own listener — confirmed in practice (this is
# what caused early "Connection reset by peer" failures right after a
# container recreate). A real PING/PONG round-trip only succeeds once
# the server is actually processing commands.
ping_ok() {
    local host="$1" port="$2"
    local resp
    resp=$(printf 'PING\r\n' | nc -w 1 "$host" "$port" 2>/dev/null)
    [[ "$resp" == *"PONG"* ]]
}

wait_for_server() {
    local timeout=30
    echo "Waiting for KV Server..."
    while ! ping_ok "$HOST" "$PORT"; do
        ((timeout--))
        if (( timeout == 0 )); then
            echo "Timed out waiting for server." >&2
            return 1
        fi
        sleep 1
    done
    echo "KV Server is ready."
}

# Default: always rebuild + force-recreate so we never silently
# benchmark a stale binary. NO_REBUILD=true opts out.
ensure_server() {
    local built_digest=""

    if [[ "${NO_REBUILD:-false}" == "true" ]]; then
        if ! check_kv_server; then
            echo "Starting KV server (--no-rebuild, not currently running)..."
            docker compose up -d || { echo "Unable to start KV server." >&2; return 1; }
            wait_for_server || return 1
        else
            echo "Reusing already-running KV server (--no-rebuild)."
        fi
    else
        echo "Building kv_store image from current checkout..."
        docker compose build kv-server || { echo "Docker build failed." >&2; return 1; }
        built_digest=$(capture_image_digest kv_store)

        echo "Recreating KV server container..."
        docker compose up -d --force-recreate kv-server || { echo "Unable to start KV server." >&2; return 1; }
        wait_for_server || return 1
    fi

    CONTAINER_IMAGE_ID=$(capture_container_image_id kv-server)

    if [[ "${NO_REBUILD:-false}" != "true" ]]; then
        if [[ "$CONTAINER_IMAGE_ID" != "$built_digest" || -z "$built_digest" || "$built_digest" == "N/A" ]]; then
            echo "ERROR: running container image ($CONTAINER_IMAGE_ID) does not match freshly built image ($built_digest)." >&2
            echo "The benchmark would not map to the current checkout. Aborting." >&2
            return 1
        fi
    fi
    BUILT_DIGEST="$built_digest"
}
