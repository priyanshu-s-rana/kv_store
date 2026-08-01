#!/usr/bin/env bash
set -uo pipefail

usage() {
    cat <<EOF
Usage: $0 <baseline_dir> <candidate_dir> [options]

Compares two redis_benchmark.sh run directories (each must contain a
schema=2 summary.csv, i.e. produced by the current redis_benchmark.sh).

Options:
  --out <path>                Where to write the comparison CSV
                               (default: ./comparison_<baseline>_vs_<candidate>.csv)
  --fail-on-regression <pct>  Exit 1 if any REAL regression exceeds this
                               percent (candidate slower than baseline).
                               Without this flag the script always exits 0.
  -h, --help                  Show this help
EOF
}

if (( $# < 2 )); then
    usage
    exit 1
fi

BASELINE_DIR="$1"
CANDIDATE_DIR="$2"
shift 2

OUT_PATH=""
FAIL_ON_REGRESSION=""

while (( $# > 0 )); do
    case "$1" in
        --out)
            OUT_PATH="$2"; shift 2 ;;
        --fail-on-regression)
            if ! [[ "$2" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
                echo "Error: --fail-on-regression requires a numeric percent." >&2
                exit 1
            fi
            FAIL_ON_REGRESSION="$2"; shift 2 ;;
        -h|--help)
            usage; exit 0 ;;
        *)
            echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

BASELINE_CSV="${BASELINE_DIR%/}/summary.csv"
CANDIDATE_CSV="${CANDIDATE_DIR%/}/summary.csv"

if [[ ! -f "$BASELINE_CSV" ]]; then
    echo "Error: $BASELINE_CSV not found." >&2
    exit 1
fi
if [[ ! -f "$CANDIDATE_CSV" ]]; then
    echo "Error: $CANDIDATE_CSV not found." >&2
    exit 1
fi

for csv in "$BASELINE_CSV" "$CANDIDATE_CSV"; do
    if ! head -n1 "$csv" | grep -q '^#schema=2'; then
        echo "Error: $csv is not a schema=2 summary.csv (produced by an old redis_benchmark.sh?)." >&2
        exit 1
    fi
done

if [[ -z "$OUT_PATH" ]]; then
    baseline_name="$(basename "${BASELINE_DIR%/}")"
    candidate_name="$(basename "${CANDIDATE_DIR%/}")"
    OUT_PATH="./comparison_${baseline_name}_vs_${candidate_name}.csv"
fi

RAW_ROWS=$(awk -F',' '
    function abs(x) { return x < 0 ? -x : x }
    FNR == 1 { next }  # skip #schema=2 comment line
    FNR == 2 {
        for (i = 1; i <= NF; i++) idx[$i] = i
        next
    }
    NR == FNR {
        key = $(idx["Command"]) SUBSEP $(idx["Concurrency"]) SUBSEP $(idx["Payload"]) SUBSEP $(idx["Keyspace"]) SUBSEP $(idx["Batch"]) SUBSEP $(idx["Pipeline"])
        b_rps[key] = $(idx["rps"])
        b_mean[key] = $(idx["rps_mean"])
        b_stddev[key] = $(idx["rps_stddev"])
        next
    }
    {
        key = $(idx["Command"]) SUBSEP $(idx["Concurrency"]) SUBSEP $(idx["Payload"]) SUBSEP $(idx["Keyspace"]) SUBSEP $(idx["Batch"]) SUBSEP $(idx["Pipeline"])
        if (!(key in b_rps)) next

        base = b_rps[key] + 0
        cand = $(idx["rps"]) + 0
        if (base == 0) next

        delta_pct = (cand - base) / base * 100

        b_relstd = (b_mean[key] + 0 != 0) ? (b_stddev[key] + 0) / (b_mean[key] + 0) : 0
        c_relstd = ($(idx["rps_mean"]) + 0 != 0) ? ($(idx["rps_stddev"]) + 0) / ($(idx["rps_mean"]) + 0) : 0
        max_rel = (b_relstd > c_relstd) ? b_relstd : c_relstd
        threshold_pct = 2 * max_rel * 100

        flag = (abs(delta_pct) > threshold_pct) ? "REAL" : "NOISE"

        n = split(key, parts, SUBSEP)
        printf "%s,%s,%s,%s,%s,%s,%.2f,%.2f,%.2f,%.2f,%s\n", parts[1], parts[2], parts[3], parts[4], parts[5], parts[6], base, cand, delta_pct, threshold_pct, flag
    }
' "$BASELINE_CSV" "$CANDIDATE_CSV")

{
    echo "Command,Concurrency,Payload,Keyspace,Batch,Pipeline,BaselineMedianRps,CandidateMedianRps,DeltaPct,ThresholdPct,Flag"
    echo "$RAW_ROWS"
} > "$OUT_PATH"

echo "Baseline:  $BASELINE_DIR"
echo "Candidate: $CANDIDATE_DIR"
echo

if command -v column >/dev/null 2>&1; then
    column -t -s, "$OUT_PATH"
else
    cat "$OUT_PATH"
fi

echo
echo "Comparison CSV written to $OUT_PATH"

if [[ -z "$RAW_ROWS" ]]; then
    echo "WARNING: no matching (Command,Concurrency,Payload,Keyspace,Batch,Pipeline) rows between the two runs." >&2
    exit 0
fi

if [[ -n "$FAIL_ON_REGRESSION" ]]; then
    regressions=$(echo "$RAW_ROWS" | awk -F',' -v thresh="$FAIL_ON_REGRESSION" '
        $11 == "REAL" && $9 < -thresh { count++ }
        END { print count + 0 }
    ')
    if (( regressions > 0 )); then
        echo
        echo "FAIL: ${regressions} REAL regression(s) exceed --fail-on-regression ${FAIL_ON_REGRESSION}%." >&2
        exit 1
    fi
fi

exit 0
