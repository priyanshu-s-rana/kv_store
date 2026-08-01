#!/usr/bin/env bash
set -uo pipefail

# Go runtime benchmarks for the hot-path packages (parser, store,
# persistence): ns/op, B/op, allocs/op straight from `go test -bench
# -benchmem`. Separate from redis_benchmark.sh / memtier_benchmark.sh,
# which measure end-to-end throughput through the real server over a real
# connection — those answer "is it faster?"; this answers "why?" (fewer
# allocations, less GC pressure, a specific component got cheaper). See
# benchmarks/README.md.
#
# Two modes, sharing the same go test invocation shape:
#   (default)   -bench -benchmem, summarised to benchmem.txt/benchmem_summary.csv
#   --profile   the same benchmarks, run once under pprof instrumentation
#               (cpu/heap/allocs/block/mutex) — see `make bench-profile`.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/common.sh
source "${SCRIPT_DIR}/../lib/common.sh"

if (( $# < 1 )); then
    echo "Usage: $0 <benchmark-name> [options]"
    echo "Run '$0 --help' for the full option list."
    exit 1
fi

# Deliberately NOT ./... — only the packages with hot-path benchmark_test.go
# files, per benchmarks/README.md. Adding a new package's benchmarks means
# adding it here.
PACKAGES=(./parser ./store ./persistence)

BENCH_REGEX="."
BENCHTIME=""
BENCHCOUNT=1
STRICT_CLEAN=false
QUICK=false
PROFILE=false

usage() {
    cat <<EOF
Usage: $0 <benchmark-name> [options]

  <benchmark-name>            Required. Results go to
                                benchmarks/<name>_<timestamp>/runtime/ (or
                                .../profile/ with --profile)

Runs 'go test -bench -benchmem' against ${PACKAGES[*]} only — never
./... — see benchmarks/README.md for why these Go-level benchmarks exist
alongside the redis-benchmark/memtier suites.

Options:
  --bench <regex>              -bench pattern (default: $BENCH_REGEX, i.e. all)
  --benchtime <spec>            -benchtime value, e.g. 2s or 500x (default: go test's own default, ~1s per benchmark)
  --count <n>                    -count value: repeat each benchmark n times (default: $BENCHCOUNT)
  --profile                     Generate pprof profiles (cpu/heap/allocs/block/mutex)
                                  instead of benchmem.txt/benchmem_summary.csv.
                                  Diagnostic use — see 'make bench-profile'.
  --strict-clean                 Abort instead of warning on a dirty working tree
  --quick                       Smoke-test mode: --benchtime=10x
  -h, --help                    Show this help
EOF
}

for arg in "$@"; do
    case "$arg" in
        --quick) QUICK=true ;;
        --profile) PROFILE=true ;;
        -h|--help) usage; exit 0 ;;
    esac
done
if $QUICK; then
    BENCHTIME="10x"
fi

POSITIONAL=()
while (( $# > 0 )); do
    case "$1" in
        --bench)
            BENCH_REGEX="$2"; shift 2 ;;
        --benchtime)
            BENCHTIME="$2"; shift 2 ;;
        --count)
            validate_positive_integer "$1" "$2" || exit 1
            BENCHCOUNT="$2"; shift 2 ;;
        --profile)
            PROFILE=true; shift ;;
        --strict-clean)
            STRICT_CLEAN=true; shift ;;
        --quick)
            shift ;;
        -h|--help)
            usage; exit 0 ;;
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

command -v go > /dev/null 2>&1 || { echo "Missing required dependency: go" >&2; exit 1; }

if git_is_dirty; then
    if $STRICT_CLEAN; then
        echo "ERROR: working tree is dirty and --strict-clean was given. Commit or stash first." >&2
        exit 1
    else
        echo "WARNING: working tree is dirty. Results will not map cleanly to a single commit." >&2
    fi
fi

# Honors a pre-set RUN_ID (e.g. from `make bench-all`, which shares one
# timestamp across all suites so results land in one directory) instead of
# always computing a fresh one.
RUN_ID="${RUN_ID:-$(date +"%Y-%m-%d_%H-%M-%S")}"

BENCH_FLAGS=(-bench="$BENCH_REGEX" -run '^$' -count="$BENCHCOUNT")
[[ -n "$BENCHTIME" ]] && BENCH_FLAGS+=(-benchtime="$BENCHTIME")

SUITE_HAD_FAILURE=0

#####################
# --profile mode
#####################
if $PROFILE; then
    OUT_DIR="benchmarks/${BENCHMARK_NAME}_${RUN_ID}/profile"
    INFO_FILE="${OUT_DIR}/benchmark_info.txt"
    mkdir -p "$OUT_DIR"

    record_env "$INFO_FILE" \
        "Mode: profile" \
        "Bench Pattern: $BENCH_REGEX" \
        "Benchtime: ${BENCHTIME:-go test default}" \
        "Count: $BENCHCOUNT" \
        "Packages: ${PACKAGES[*]}"

    for pkg in "${PACKAGES[@]}"; do
        pkg_name="${pkg#./}"
        pkg_dir="${OUT_DIR}/${pkg_name}"
        mkdir -p "$pkg_dir"
        echo "Profiling ${pkg}..."

        if ! go test "$pkg" -benchmem "${BENCH_FLAGS[@]}" \
            -cpuprofile="${pkg_dir}/cpu.pprof" \
            -memprofile="${pkg_dir}/heap.pprof" \
            -blockprofile="${pkg_dir}/block.pprof" \
            -mutexprofile="${pkg_dir}/mutex.pprof" \
            &> "${pkg_dir}/go_test_output.txt"; then
            echo "  Profiling failed for ${pkg} — see ${pkg_dir}/go_test_output.txt" >&2
            SUITE_HAD_FAILURE=1
            continue
        fi

        # go test's -memprofile writes one profile covering both
        # allocation and in-use samples — the same underlying data
        # net/http/pprof's own /debug/pprof/heap and /debug/pprof/allocs
        # endpoints both draw from. There's no separate "allocs-only"
        # profiler to invoke, so copy it under both names; the split is in
        # which pprof view you ask for at inspection time (see
        # benchmarks/README.md): `-inuse_space` for heap.pprof,
        # `-alloc_objects`/`-alloc_space` for allocs.pprof.
        cp "${pkg_dir}/heap.pprof" "${pkg_dir}/allocs.pprof"
    done

    echo
    if (( SUITE_HAD_FAILURE == 1 )); then
        echo "Profiling completed WITH FAILURES. See ${OUT_DIR}/*/go_test_output.txt" >&2
        echo "Results saved to $OUT_DIR"
        exit 1
    fi

    echo "Profiling completed."
    echo "Results saved to $OUT_DIR"
    exit 0
fi

#####################
# Default mode: benchmem summary
#####################
OUT_DIR="benchmarks/${BENCHMARK_NAME}_${RUN_ID}/runtime"
INFO_FILE="${OUT_DIR}/benchmark_info.txt"
RAW_FILE="${OUT_DIR}/benchmem.txt"
SUMMARY_FILE="${OUT_DIR}/benchmem_summary.csv"
mkdir -p "$OUT_DIR"

record_env "$INFO_FILE" \
    "Bench Pattern: $BENCH_REGEX" \
    "Benchtime: ${BENCHTIME:-go test default}" \
    "Count: $BENCHCOUNT" \
    "Packages: ${PACKAGES[*]}" \
    "Quick Mode: $QUICK"

: > "$RAW_FILE"
{
    echo "#schema=1"
    echo "Package,Benchmark,ns/op,B/op,allocs/op"
} > "$SUMMARY_FILE"

echo "Running Go runtime benchmarks '${BENCHMARK_NAME}': bench=${BENCH_REGEX} count=${BENCHCOUNT} benchtime=${BENCHTIME:-default}"

for pkg in "${PACKAGES[@]}"; do
    pkg_name="${pkg#./}"
    echo "Package: ${pkg}"
    echo "=== ${pkg} ===" >> "$RAW_FILE"

    # go test merges the compiled test binary's stdout and stderr itself
    # before this process ever sees it, so shell-level fd redirection
    # can't separate them — benchmarked code that log.Printfs mid-run
    # (e.g. persistence.NewAOF, on the default logger) can still land
    # mid-line, splitting "BenchmarkAOFAppend-8\t" from its "N ns/op ..."
    # result onto what become two separate physical lines. The awk below
    # is written to tolerate that: it remembers the last "Benchmark*" name
    # it saw and pairs it with the next line containing "ns/op", rather
    # than requiring both on the same line.
    out_file="$(mktemp)"
    if ! go test "$pkg" -benchmem "${BENCH_FLAGS[@]}" > "$out_file" 2>&1; then
        echo "  go test failed for ${pkg} — see ${RAW_FILE}" >&2
        SUITE_HAD_FAILURE=1
    fi
    cat "$out_file" >> "$RAW_FILE"

    awk -v pkg="$pkg_name" '
        /^Benchmark[^ \t]+/ {
            match($0, /^Benchmark[^ \t]+/)
            pending = substr($0, RSTART, RLENGTH)
        }
        /ns\/op/ {
            if (pending == "") next
            name = pending
            sub(/-[0-9]+$/, "", name)
            ns = ""; b = ""; allocs = ""
            for (i = 1; i <= NF; i++) {
                if ($i == "ns/op") ns = $(i-1)
                else if ($i == "B/op") b = $(i-1)
                else if ($i == "allocs/op") allocs = $(i-1)
            }
            printf "%s,%s,%s,%s,%s\n", pkg, name, ns, b, allocs
            pending = ""
        }
    ' "$out_file" >> "$SUMMARY_FILE"

    rm -f "$out_file"
done

if command -v column >/dev/null 2>&1; then
    echo
    echo "Summary:"
    tail -n +2 "$SUMMARY_FILE" | column -t -s,
fi

echo
if (( SUITE_HAD_FAILURE == 1 )); then
    echo "Runtime benchmark completed WITH FAILURES. See ${RAW_FILE}" >&2
    echo "Results saved to $OUT_DIR"
    exit 1
fi

echo "Runtime benchmark completed."
echo "Results saved to $OUT_DIR"
